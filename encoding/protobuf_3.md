# Protobuf 3.0 - 编码

## 1. 前言

Protobuf 是 G 厂开源的序列化数据的方法，可用来通信或者存储数据。它采用 IDL 描述数据接口，使得不同语言编写的程序可以根据同一接口通信。不同编程语言也可以根据 IDL 的描述来生成对应数据结构，该数据结构用来编解码。为此，G 厂为主流开发语言都提供代码生成器（即 protoc ）。

为了更好地了解一些细节，本文将主要描述 Protobuf 3.0 的编码规则。

> Protobuf 采用是 Little Endian 的方式编码。

## 2. 热身

Protobuf 里面主要采用 Varint 和 Zig-Zag 的方式来对整型数字进行编码。在理解 Protobuf 之前，需要先了解这两种编码方式。

## 2.1 Varints

int64, int32, uint64, uint32 都有固定的二进制位数。

如果将这些数字序列化成二进制流的时候，需要额外空间告知接收方数据的长度。对于采用 int64, uint64 这两种类型的数据而言，如果大部分时间都只是使用较小的数值，那么会极大地浪费传输带宽和存储空间。针对这两个问题，Protobuf 采用 Varints 的编码方式。

Varints 将源数据按照 **7 bit** 分组，每 **7 bit** 加 **MSB (Most Significant Bit)** 标识位来组成一个字节，其中 MSB 标识位用来判断是否存在后序分组。如果出现多组的情况，那么低有效位比特组优先。

64 有效位为 7 bit，不需要额外的字节，所以 MSB 比特位为 0。

```
64 = 0100 0000
  => 0100 0000
```

16657 有效位为 15 bit，需要分成三组字节，前两组字节为了提示还存在后续字节，所以前两组字节的 MSB 比特位为 1。

```
16657 = 0100 0001 0001 0001
  =>  001 0001 ++ 000 0010 ++ 000 0001 (reverse the groups of 7 bits)
  =>  1001 0001 1000 0010 0000 0001
  =>  0x91 0x82 0x01
```

由于负数的最高有效位为 1，int32 类型的负数固定需要 5 字节，而 int64 的负数需要 10 字节，基本上告别了空间效益。所以 Varints 在编码负数时，需要引入 Zig-Zag 编码解决压缩问题。

## 2.2 Zig-Zag

| Signed Original | Encoded As      |
| :-------------: | :-------------: |
| 0               | 0               |
| -1              | 1               |
| 1               | 2               |
| -2              | 3               |
| 2               | 4               |
| 2147483647      | 4294967294      |
| -2147483648     | 4294967295      |

Zig-Zag 编码可以将负数转化成正数，如上表所示。根据上述表格可以很快地得出结论，负数 `n` 编码后的值为 `2 * abs(n) - 1`，而正数，编码后为 `2 * n`。

实际上，Zig-Zag 会采用以下方式来进行编解码。为了简单起见，接下来将使用 **int8** 类型分析 Zig-Zag 编解码。

```
encode(n):
  int64 => (n << 1) ^ (n >> 63)
  int32 => (n << 1) ^ (n >> 31)
  int64 => (n << 1) ^ (n >> 15)
  int8  => (n << 1) ^ (n >> 7)

decode(n): 
  (n >>> 1) ^ - (n & 1)

NOTE: 
  <<, >> Arithmetic Shift
  >>>       Logical Shift
```

### 2.2.1 Encode

Zig-Zag 会将最高符号位算数位移到 **LSB（Least significant bit）**。

```
positive: (n >> 7) => 0x00
negative: (n >> 7) => 0xFF
```

任何数值与 `0x00` 异或都等到本身，而与 `0xFF` 异或会现成按位取反。
根据补码互补的原理，一个数 `A` 与 `0xFF` 异或就变成 `-A - 1`。

```
A ^ 0xFF = ~A = -A - 1
```

因此，负数经过运算之后变成 `- 2 * n - 1`。而正数经过运算只是简单扩大两倍而已，将会 `2 * n`。 

```
-2 => 3

  1111 1100 (1111 1110 << 1)
^ 1111 1111 (1111 1110 >> 7)
-----------------
  0000 0011 (-2 << 1) ^ (-2 >> 7)
```


### 2.2.2 Decode

Zig-Zag 编码的时候将最高符号位移位到了 LSB，解码的时候需要还原到 MSB。

```
positive: - (n & 1) = 0  => 0x00
negative: - (n & 1) = -1 => 0xFF
```

`n >>> 1` 逻辑右移的过程相当于做了除以 2 的操作，所有奇数的逻辑右移都可以得到 `n / 2 = (n - 1)/2`，根据解码的表达式可以得到以下推断。

```
n & 1 == 0:
  (n >>> 1) ^ -(n & 1) = (n >>> 1) = n / 2

n & 1 == 1:
  (n >>> 1) ^ -(n & 1) = ~(n >>> 1) = - (n >>> 1) - 1 = - (n + 1) / 2
```

`255` 解码之后的结果为 `-128`。如果解码过程是通过先加后除的方式，将会出现溢出错误。

```
255 => -128

  0111 1111 (1111 1111 >>> 1)
^ 1111 1111 (-(1111 1111 & 1))
-----------------
  1000 0000 (255 >>> 1) ^ -(255 & 1)
```

### 2.3 小结

Protobuf 在编码负数的时候，它提供了 Zig-Zag 编码的可能，可在此基础上在使用 Varints 来达到压缩效果。


## 3. Message

```
message Simple {
  //
  //     _ declared type
  //    /      _ field name
  //   /      /     _ field number, alias tag
  //  /      /     /
  int64 o_int64 = 16;
}
```

Protobuf Message 是一系列的 Key-Value 二进制数据流。在编码过程中，仅仅使用 **field number** 和 **wire type** 为 Key，而 **declared type** 和 **field name** 会辅助解码来判断数据的具体类型，其中 wire type 有以下几种类型。

| Wire Type       | Meaning          | Used For                                                  |
| :-------------: | :-------------:  | :------------------------------------------------------:  |
| 0               | Varint           | int32, int64, uint32, uint64, sint32, sint64, bool, enum  |
| 1               | 64-bit           | fixed64, sfixed64, double                                 |
| 2               | Length-delimited | string, bytes, embedded messages, packed repeated fields |
| 3               | Start Group      | groups (deprecated)                                       |
| 4               | End Group        | groups (deprecated)                                       |
| 5               | 32-bit           | fixed32, sfixed64, float                                  |

每一个 Key 都是 `(field number << 3 | wire type)` 的 Varint 编码值。

现在按照 Simple 的约定发送来以下数据。接下来，我们将作为人工解码器来分析这份数据。

```
80 01 96 01
```

首先 Protobuf Message 编码之后是一系列的 Key-Value，因此首字节属于 Key 的一部分。Key 首字节 `80` 的 MSB 标志位为 1，说明 Key 除了 `80` 外还有后序字节。根据上文 Varints 的介绍，可以得到 Key 中 field number(`16`) 和 wire type(`0`)。  

```
80 01

1000 0000 0000 0001
   => 000 0000 ++ 000 0001  (drop the msb)
   => 1000 0000             (reverse the groups of 7 bits)
   => (0001 0000 << 3) | 0
```

按照同样方式，Value 数据为 `96 01`。经过 Varints 解码后为 150，所以 `80 01 96 01` 代表着 `Simple.o_int64 = 150`。

```
96 01

1001 0110 0000 0001
  => 001 0110 ++ 000 0001    (drop the msb)
  => 1001 0110               (reverse the groups of 7 bits)
  => 128 + 16 + 4 + 2 = 150
```

## 4. Wire Type

### 4.1 Varint - sint32/sint64

对于负数而言，前序比特 1 不能带来压缩上效益，所以 Protobuf 提供 `sint32`，`sint64` 类型来使用 Zig-Zag 提高压缩率。

### 4.2 32-bit / 64-bit

这两部分 wire type 会使用固定长度去传输数据，其中 `64-bit` 采用 8 字节传输，而 `32-bit` 采用 4 字节传输。

### 4.3 Length-delimited

Length-delimited 会引入 **payload size** 来辅助说明后序字节数，其中 **payload size** 的编码采用 Varints 。

#### 4.3.1 strings/bytes

```
message SimpleString {
  string o_string = 1;
}
```

将 `o_string` 设置成 `Hello, world!`，会得到以下数据。

```
0A 0D 48 65 6C 6C 6F 2C 20 77 6F 72 6C 64 21
```

Key `0A` 可以推断出 field number(`1`) 和 wire type(`2`)。payload size(`0D`) 解码之后为 13 ，后序 13 个字节将代表 `o_string`。

#### 4.3.2 embedded messages

```
message SimpleEmbedded {
  Simple o_embedded = 1; 
}
```

将 `o_embedded.o_int64` 设置成 150，会得到以下数据。

```
0A 04 80 01 96 01
```

Key `0A` 可以推断出 field number(`1`) 和 wire type(`2`)。payload size(`04`)  解码之后为 4 ，后序 4 个字节将代表 `o_embedded`。整个过程基本和 SimpleString 一致，只不过 `o_embedded` 还需要进一步的解码。

#### 4.3.3 packed repeated fields

Protobuf 3.0 对于 repeated field 默认都采用了 packed 的形式。不过在介绍 packed 特性前，有必要说明一下 unpacked 的编码结构。

```
message SimpleInt64 {
  int64 o_int64 = 1;
}

message SimpleUnpacked {
  repeated int64 o_ids = 1 [packed = false];
}

message SimplePacked {
  repeated int64 o_ids = 1;
}
```

将 `SimpleUnpacked.o_ids` 设置成 `1,2` 数组，会得到以下数据。

```
08 01 08 02

08 // field number = 1, wire type = 0
01 // value = 1 
08 // field number = 1, wire type = 0
02 // value = 2
```

Protobuf 编码 unpacked repeated fields 时，并不会将 repeated fields 看成是一个整体，而是单独编码每一个元素。所以在解码 unpacked repeated fileds 时，需要将相同 field number 的数据合并到一起。

从另外一个角度看，Protobuf 允许将相同 Key 的数据合并到一起。`08 01 08 02` 数据可以看成是 `SimpleInt64.o_int64 = 1` 和 `SimpleInt64.o_int64 = 2` 编码合并的结果。

让我们来看看 packed repeated fields 编码结果。同样将 `SimplePacked.o_ids` 设置成 `1,2` 数组，却得到不同的数据，因为 Protobuf 编码时将 `o_ids` 看成是一个整体。

```
0A 02 01 02

0A // field number = 1, wire type = 2
02 // payload size = 2
01 // first elem = 1
02 // second elem = 2
```

Protobuf 3.0 packed 的行为仅仅支持基础数据类型，即 `Varint/64-bit/32-bit` 三种 wire type。

packed 和 unpacked 编码面对长度为 0 的数据时，它并不会输出任何二进制数据。

> 个人认为基础数据类型所占用字节数少，整体字节数相对可控，引入 payload size 能带来压缩效益。一旦使用 embedded message 之后，每一个元素的大小将不可控，可能只有少量元素，但是整体字节数将会很大，payload size 需要大量的字节表示。面对这种场景，unpacked repeated fields 单独编码的方式会带来压缩效益，即使包含了重复的 Key 信息。

### 4.4 Start Group/End Group

由于 Protobuf 放弃使用 `Start Group` 和 `End Group`，在此也不再介绍。

## 5. Reference

* [Protocol Buffers Encoding](https://developers.google.com/protocol-buffers/docs/encoding)
