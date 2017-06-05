# interface & Duck Typing

最后更新时间：`2017-06-05`

标签：`Golang` `Interface` `Duck Typing`

Go 不需要像 Java 那样显式地使用 **implement** 说明某一数据类型实现了 interface，只要某一数据类型实现了 interface 所定义的方法名签，那么就称该数据类型实现了 interface。interface 的语言特性可以容易地做到接口定义和具体实现解耦分离，并将注意力转移到如何使用 interface ，而不是方法的具体实现，我们也称这种程序设计为 Duck Typing。文本将描述 Go 是如何通过 interface 来实现 Duck Typing。

> 本文提供的源代码都是基于 go1.7rc6 版本。

## 1. Duck Typing

了解实现原理之前，我们可以简单过一下 Go 的 Duck Typing 示例。

```
package main

type Ducker interface { Quack() }

type Duck struct {}
func (_ Duck) Quack() { println("Quaaaaaack!") }

type Person struct {} 
func (_ Person) Quack() { println("Aha?!") }

func inTheForest(d Ducker) { d.Quack() }
	
func main() {
	inTheForest(Duck{})
	inTheForest(Person{})
}

// result:
// Quaaaaaack!
// Aha?!
```
在示例中，`inTheForest`函数使用了`Ducker`的`Quack()`方法，而`Quack()`方法的具体实现由实参所决定。根据 Go interface 的定义，`Duck`和`Person`两种数据类型都有`Quack()`方法，说明这两种数据类型都实现了`Ducker`。当实参分别为这两种类型的数据时，`inTheForest`函数表现出『多态』。

在这没有继承关系的情况下，Go 可以通过 interface 的 Duck Typing 特性来实现『多态』。作为一个静态语言，Go 是如何实现 Duck Typing 这一特性？

## 2. interface data structure

interface 是 Go 数据类型系统中的一员。在分析运行机制之前，有必要先了解 interface 的数据结构。

### 2.1 empty interface

```
// src/runtime/runtime2.go
type eface struct {
    _type *_type
    data  unsafe.Pointer
}
```

当一个 interface 没有定义方法签名时，那么我们称之为 empty interface。它由`_type`和`data`组成，其中`data`表示 interface 具体实现的数据，而`_type`是`data`对应数据的类型元数据。因为没有定义方法签名，所以任何类型都『实现』empty interface。换句话来说，empty interface 可以接纳任何类型的数据。

```
package main

func main() {
    i := 1
    var eface interface{} = i
    
    println(eface)
}

// gdb info
// (gdb) i locals
// i = 1
// eface = {
//   _type = 0x55ec0 <type.*+36000>,
//   data = 0xc420045f18
// }
// (gdb) x/x eface.data
// 0xc420045f18:   0x00000001
// (gdb) x/x &i
// 0xc420045f10:   0x00000001
```
在使用 gdb 来查看`eface`数据结构的过程中，我们会发现比较特别的一点：`eface.data`和`i`的地址不同。一般情况下，将一个数据赋值给 interface 时，程序会为数据生成一份副本，并将副本的地址赋给`data`。

```
// src/cmd/compile/internal/gc/subr.go
// Can this type be stored directly in an interface word?
// Yes, if the representation is a single pointer.
func isdirectiface(t *Type) bool {
    switch t.Etype {
    case TPTR32,
        TPTR64,
        TCHAN,
        TMAP,
        TFUNC,
        TUNSAFEPTR:
        return true

    case TARRAY:
        // Array of 1 direct iface type can be direct.
        return t.NumElem() == 1 && isdirectiface(t.Elem())

    case TSTRUCT:
        // Struct with 1 field of direct iface type can be direct.
        return t.NumFields() == 1 && isdirectiface(t.Field(0).Type)
    }

    return false
}
```
当一个数据的类型符合`isdirectiface`的判定时，那么程序不会生成副本，而是直接将实际地址赋给`data`。由于这部分内存分配优化和 **reflect** 实现有关，在此就不做展开描述了。

> reflect 要想在运行时解析数据的方法和属性，它就需要知道数据以及类型元数据。而 empty interface 正好能满足这一需求，这也正是 reflect 的核心方法`ValueOf`和`TypeOf`的形参是 empty interface 的原因。

在 Duck Typing 的使用上，empty interface 使用频率比较高的场景是 Type Switch, Type Assertion，接下来会介绍这些使用场景。

### 2.2 non-empty interface

相对于 empty interface 而言，有方法签名的 interface 的数据结构要复杂一些。

```
// src/runtime/runtime2.go
type iface struct {
    tab  *itab
    data unsafe.Pointer
}

type itab struct {
    inter  *interfacetype
    _type  *_type
    link   *itab
    bad    int32
    unused int32
    fun    [1]uintptr // variable sized
}
```

`iface`包含两个字段 `tab` 和 `data`。和 empty interface 一样，`data`表示具体实现的数据。`tab`不再是简单的`_type`，不仅维护了（`interfacetype`，`_type`）匹配的信息，还维护了具体方法实现的列表入口`fun`。

> 其中`interfacetype`是相应 interface 类型的元数据。
> 
> 而`fun`字段是一个变长数组的 header ，它代表着具体方法数组的头指针，程序通过`fun`去定位具体某一方法实现。

来看看下面这一段程序。

```
package main

type Ducker interface {
        Quack()
        Feathers()
}

type Duck struct{ x int }

func (_ Duck) Quack() { println("Quaaaaaack!") }

func (_ Duck) Feathers() { println("The duck has white and gray feathers.") }

func inTheForest(d Ducker) {
        d.Quack()
        d.Feathers()
}

func main() {
        inTheForest(Duck{x: 1})
}

// gdb info at func inTheForest
(gdb) p d
$2 = {
  tab = 0x97100 <Duck,main.Ducker>,
  data = 0xc42000a118
}
(gdb) x/2xg d.tab.fun
0x97120 <go.itab.main.Duck,main.Ducker+32>:     0x00000000000022f0      0x0000000000002230
(gdb) i symbol 0x00000000000022f0
main.(*Duck).Feathers in section .text
(gdb) i symbol 0x0000000000002230
main.(*Duck).Quack in section .text
```

在`inTheForest`函数里，`d.tab.fun`数组包含了`Duck`的`Quack`以及`Feathers`的方法地址，因此在`d.Quack()`和`d.Feathers()`分别使用了`Duck`的`Quack`和`Feathers`方法的具体实现。假如这个时候，传入的不是`Duck`，而是其他实现了`Ducker`的数据类型，那么`d.tab.fun`将会包含相应类型的具体方法实现。

>  d.tab.fun 不会包含 interface 定义以外的方法地址。

不难发现，`itab.fun`包含了具体方法的实现，程序在运行时通过`itab.fun`来决议具体方法的调用，这也是实现 Duck Typing 的核心逻辑。那么问题来了，`itab`是什么时候生成的？

## 3. itab

当数据类型`Duck`实现了`Ducker`中的所有方法时，编译器才会生成`itab`，并将`Duck`对`Ducker`的具体实现绑定到`itab.fun`上，否则编译不通过。`itab.fun`很像 C++ 中的虚函数表。而 Go 没有继承关系，一个 interface 就可能会对应 N 种可能的具体实现，这种 M:N 的情况太多，没有必要去为所有可能的结果生成`itab`。因此，编译器只会生成部分`itab`，剩下的将会在运行时生成。

> C++ 通过继承关系，在编译期间就生成类的虚函数表。在运行状态下，通过指针来查看虚函数表来定位具体方法实现。

当一个数据类型实现 interface 中所声明的所有方法签名，那么`iface`就可以携带该数据类型对 interface 的具体实现，否则将会 panic 。这部分判定需要`_type`和`interfacetype`元数据，而这部分数据在编译器已经为运行时准备好了，那么判定和生成`itab`就只要照搬编译器里那一套逻辑即可。

```
// src/runtime/iface.go
var (
    ifaceLock mutex // lock for accessing hash
    hash      [hashSize]*itab
)

func itabhash(inter *interfacetype, typ *_type) uint32 {...}
func getitab(inter *interfacetype, typ *_type, canfail bool) *itab {...}
func additab(m *itab, locked, canfail bool) {...}

// src/runtime/runtime2.go
type itab struct {
    inter  *interfacetype
    _type  *_type
    link   *itab
    bad    int32
    unused int32
    fun    [1]uintptr // variable sized
}
```

为了保证运行效率，程序会在运行时会维护全局的`itab` hash 表，`getitab`会在全局 hash 表中查找相应的`itab`。当`getitab`发现没有相应的`itab`时，它会调用`additab`来添加新的`itab`。在插入新的`itab`之前，`additab`会验证`_type`对应的类型是否都实现了`interfacetype`声明的方法集合。

> 运行时通过`itabhash`负责生成 hash 值，并使用单链表来解决冲突问题，其中`itab.link`可用来实现链表。

那么问题又来了，`_type`有 N 个方法，`interfacetype`有 M 个方法签名，验证匹配的最坏可能性就是需要 N * M 次遍历。除此之外，`additab`在写之前需要加锁，这两方面都会影响性能。

### 3.1 additab 的效率问题

为了减少验证的时间，编译期间会对方法名进行排序，这样最坏的可能也就需要 N + M 次遍历即可。

> 细心的朋友可能会发现，在上一个例子中`d.tab.fun`中的方法是按照字符串大小排序的。

```
// src/runtime/iface.go
func additab(m *itab, locked, canfail bool) {
    inter := m.inter
    typ := m._type
    x := typ.uncommon()

    // both inter and typ have method sorted by name,
    // and interface names are unique,
    // so can iterate over both in lock step;
    // the loop is O(ni+nt) not O(ni*nt).
    ...
}
```

###  3.2 锁的效率问题

关于锁的问题，在实现`getitab`的时候，引入了两轮查询的策略。因为`itab`数据比较稳定，引入两轮查询可以减少锁带来的影响。

```
// src/runtime/iface.go
func getitab(inter *interfacetype, typ *_type, canfail bool) *itab {
    ....
    // look twice - once without lock, once with.
    // common case will be no lock contention.
    var m *itab
    var locked int
    for locked = 0; locked < 2; locked++ {
        if locked != 0 {
            lock(&ifaceLock)
        }
        ...
     }
     ...
}
```

`itab`的生成和查询或多或少带有运行时的开销。然而`itab`不仅提供了静态语言的类型检查，还提供了动态语言的灵活特性。只要不滥用 interface，`itab`还是可以提供不错的编程体验。


## 4. Type Switch & Type Assertion

开发者会使用 interface 的 Type Switch 和 Type Assertion 来进行『类型转化』。

```
package main

type Ducker interface { Feathers() }

type Personer interface { Feathers() }

type Duck struct{}

func (_ Duck) Feathers() { /* do nothing */ }

func example(e interface{}) {
	if _, ok := e.(Personer); ok {
		println("I'm Personer")
	}
	
	if _, ok := e.(Ducker); ok {
		println("I'm Ducker")
	}
}

func main() {
     var d Ducker = Duck{}
     example(d)
}

// result:
// I'm Personer
// I'm Ducker
```

根据之前对`itab`的分析，其实`e.(Personer)`和`e.(Ducker)`这两个断言做的就是切换`itab.inter`和`itab.fun`，并不是动态语言里的『类型转化』。那么断言的函数入口在哪？

```
// go tool objdump -s 'main.example' ./main
        main.go:15      0x2050  65488b0c25a0080000      GS MOVQ GS:0x8a0, CX
        main.go:15      0x2059  483b6110                CMPQ 0x10(CX), SP
        main.go:15      0x205d  0f86eb000000            JBE 0x214e
        main.go:15      0x2063  4883ec38                SUBQ $0x38, SP
        main.go:15      0x2067  48896c2430              MOVQ BP, 0x30(SP)
        main.go:15      0x206c  488d6c2430              LEAQ 0x30(SP), BP
        main.go:16      0x2071  488d05c8840500          LEAQ 0x584c8(IP), AX
        main.go:16      0x2078  48890424                MOVQ AX, 0(SP)
        main.go:16      0x207c  488b442448              MOVQ 0x48(SP), AX
        main.go:16      0x2081  488b4c2440              MOVQ 0x40(SP), CX
        main.go:16      0x2086  48894c2408              MOVQ CX, 0x8(SP)
        main.go:16      0x208b  4889442410              MOVQ AX, 0x10(SP)
        main.go:16      0x2090  48c744241800000000      MOVQ $0x0, 0x18(SP)
     => main.go:16      0x2099  e892840000              CALL runtime.assertE2I2(SB)
        main.go:16      0x209e  0fb6442420              MOVZX 0x20(SP), AX
        main.go:16      0x20a3  8844242f                MOVB AL, 0x2f(SP)
```

通过`objdump`发现一个很特别的方法：`runtime.assertE2I2`。`assertE2I2`是一个断言函数，它负责判断一个 empty interface 里的数据能否转化成一个 non-empty interface，名字最后那个`2`代表着有两个返回值：

* 第一参数是转化后的结果
* 第二参数是断言结果

接下来看看`assertE2I2`的源码。

```
// src/runtime/iface.go
func assertE2I2(inter *interfacetype, e eface, r *iface) bool {
    if testingAssertE2I2GC {
        GC()
    }
    t := e._type
    if t == nil {
        if r != nil {
            *r = iface{}
        }
        return false
    }
    tab := getitab(inter, t, true)
    if tab == nil {
        if r != nil {
            *r = iface{}
        }
        return false
    }
    if r != nil {
        r.tab = tab
        r.data = e.data
    }
    return true
}
```

该函数会拿出 empty interface 中的`_type`和`interfacetype`在`getitab`中做查询和匹配验证。如果验证通过，`r`会携带转化后的结果，并返回`true`。否则返回`false`。

`src/runtime/iface.go`中还有很多类似`assertE2I2`的函数，在这里就不一一阐述了。

```
// T: 具体的数据类型_type
// E: empty interface
// I:  non-empty interface

// src/runtime/iface.go
func assertE2I(inter *interfacetype, e eface, r *iface) {...}
func assertI2I2(inter *interfacetype, i iface, r *iface) bool {..}
func assertI2E(inter *interfacetype, i iface, r *eface) {...}
func assertI2E2(inter *interfacetype, i iface, r *eface) bool {...}
func assertE2T2(t *_type, e eface, r unsafe.Pointer) bool {..}
func assertE2T(t *_type, e eface, r unsafe.Pointer) {..}
func assertI2T2(t *_type, i iface, r unsafe.Pointer) bool {...}
func assertI2T(t *_type, i iface, r unsafe.Pointer) {...}

func convI2I(inter *interfacetype, i iface) (r iface) {...}
func convI2E(i iface) (r eface) {...}
```

## 5. 最后

interface 的 Duck Typing 可以用来实现『多态』、代码的模块化。但是这毕竟有运行时的开销，interface 的滥用和声明大量的方法签名还是会影响到性能。

## 6. Reference

* [Duke Typing](https://en.wikipedia.org/wiki/Duck_typing)
* [C++ 虚函数表解析](http://coolshell.cn/articles/12165.html)
* [Go Data Structures: Interfaces](https://research.swtch.com/interfaces)
