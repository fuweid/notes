# interface & duck typing

Go 语言的类型系统最重要的一环当属 interface。

作为非面向对象编程的静态语言来说，Interface 的 Duck Typing 特性能很轻松地完成『多态』的特性，这也是 Go 语言很特别的地方。本文主要围绕『多态』来描述 interface 的具体实现细节。

> 本文提供的源代码都是基于 go1.7rc6 版本。

## 1. 多态

interface 描述的是方法签名的集合。

它通过统一的方法签名，来屏蔽具体实现细节和减少对具体类型的依赖，从而达到『多态』的效果，也称之为 Duck Typing。


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

不同语言实现『多态』的方式还是存在一定差异。

在 C++ 语言里，编译器编译时会生成虚函数表，然后在运行时决策，通过父类类型指针来使用子类的成员函数。
而 Ruby 却和 C++ 不同，它是动态类型语言，程序会在运行时查找方法链来定位具体成员函数。

那么 Go 是怎么通过 interface 来实现『多态』的呢？

## 2. interface value

interface 是 Go 类型系统中的一种，在分析『多态』的运行机制之前，需要了解 interface 在程序中的数据结构。

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

当一个**包含方法签名**的 interface 作为左值时，程序的运行时会使用`iface`来表示一个 interface 。

`iface`包含两个字段 `tab` 和 `data`。其中`data`所指向的地址存放了右值的具体数据，而`itab`中的`fun`字段是一个变长数组的 header ，它代表着具体方法实现的入口，程序可以在运行时通过`fun`去定位具体实现。

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
(gdb) x/x d.data
0xc42000a118:   0x00000001
(gdb) x/2xg d.tab.fun
0x97120 <go.itab.main.Duck,main.Ducker+32>:     0x00000000000022f0      0x0000000000002230
(gdb) i symbol 0x00000000000022f0
main.(*Duck).Feathers in section .text
(gdb) i symbol 0x0000000000002230
main.(*Duck).Quack in section .text
```

在`inTheForest`函数里，`d.tab.fun`数组包含了`Duck`的`Quack`以及`Feathers`的方法地址，因此在`d.Quack()`和`d.Feathers()`分别使用了`Duck`的`Quack`和`Feathers`的具体实现。

假如这个时候，传入的不是`Duck`，而是其他类型，那么`d.tab.fun`将会包含相应类型的方法地址。

>  d.tab.fun 不会包含 interface 定义以外的方法地址。

简单分析之后，我们可以发现`itab`维护着`Ducker`和`Duck`之间的关系，是实现 Duck Typing 的关键。

那么问题来了，`itab`是什么时候生成的？

## 3. itab in runtime

编译器会为所有数据类型生成相应的元数据，interface 对应着`interfacetype`，而其他数据类型对应着`_type`。

拿上一个例子说，当数据类型`Duck`实现了`Ducker`中的所有方法时，编译器才会生成`itab`并绑定上`Duck`的具体实现，否则编译不通过。

`itab`很像 C++ 中的虚函数表。

> C++ 通过继承关系，在编译期间就生成好所有基类的虚函数表，在运行状态下，只要查看虚函数表就可以实现『多态』。

而 Go 没有继承关系，一个 interface 就可能会对应 N 种可能的具体实现，这种 M:N 的情况太多，也没有必要去为所有可能的结果生成`itab`。

所以编译器只会生成部分`itab`，剩下的将会在运行时生成。这里包含两个维度的问题：

* 怎么生成
* 什么时候需要


### 3.1 该如何生成

当一个数据类型实现 interface 中所声明的所有方法签名，那么`iface`就可以携带该数据类型对 interface 的具体实现，否则将会 panic 。

这部分判定需要`_type`和`interfacetype`元数据，而这部分数据在编译器已经为运行时准备好了，那么判定和生成`itab`就只要照搬编译器里那一套逻辑即可。

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
    ...
    link   *itab
    ...
}
```

程序在运行时会维护全局的`itab`表，会将 hash 值一致的`itab`保存在同一个链表中，`getitab`会在全局表中查找相应的`itab`。

> itab 中的 link 用来实现单链表。

当`getitab`发现没有相应的`itab`时，它会调用`additab`来添加新的`itab`。在插入新的`itab`之前，`additab`会验证`_type`对应的类型是否都实现了`interfacetype`声明的方法集合。

那么问题又来了，`_type`有 N 个方法，`interfacetype`声明了 M 个方法，最坏的可能性就是需要 N * M 次遍历。而且写之前需要加锁，这两方面都是会影响性能。

#### 3.1.1 additab 的效率问题

为了减少验证的时间，编译期间会对方法列表进行排序，这样最坏的可能也就需要 N + M 次遍历即可。

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

####  3.1.2 锁的效率问题

关于锁的问题，在实现`getitab`的时候，引入了两轮查询的策略。因为这部分`itab`数据比较稳定，引入两轮查询可以减少锁带来的影响。

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


### 3.2 什么时候需要

比较常见的一种场景就是 Type Assertion。我们来看看下面一段代码。

```
package main

type Ducker interface { Feathers() }

type Personer interface { Feathers() }

type Duck struct{}

func (_ Duck) Feathers() { println("The duck has white and gray feathers.") }

func main() {
        var d Ducker = Duck{}

        if _, ok := d.(Personer); ok {
                println("I'm Personer")
        }
}

// result:
// I'm Personer
```

编译器为`d`为`Ducker`和`Duck`生成了一个`itab`，但是并没有为`Personer `和`Duck`生成相应的`itab`。
对应的`itab`是在 Type Assetion 的时候生成的，该例子对应的方法是 `assertI2I2`。

```
// src/runtime/iface.go
func assertI2I2(inter *interfacetype, i iface, r *iface) bool {
    tab := i.tab
    if tab == nil {
        if r != nil {
            *r = iface{}
        }
        return false
    }
    if tab.inter != inter {
        tab = getitab(inter, tab._type, true)
        if tab == nil {
            if r != nil {
                *r = iface{}
            }
            return false
        }
    }
    if r != nil {
        r.tab = tab
        r.data = i.data
    }
    return true
}
```

Type Assertion, Type Switch 相关的方法都在`src/runtime/iface.go`中，在这里就不一一阐述了。

## 4 empty interface 和 data

到目前为止，有关于『多态』的部分就描述完了，但 interface 还有两个重要设计：

* empty interface
* data

### 4.1 empty interface

当一个 interface 没有生命方法签名的时候，Go 将其定义为 empty interface。

```
// src/runtime/runtime2.go
type eface struct {
    _type *_type
    data  unsafe.Pointer
}

// src/runtime/type.go
// Needs to be in sync with ../cmd/compile/internal/ld/decodesym.go:/^func.commonsize,
// ../cmd/compile/internal/gc/reflect.go:/^func.dcommontype and
// ../reflect/type.go:/^type.rtype.
type _type struct {
    size       uintptr
    ptrdata    uintptr // size of memory prefix holding all pointers
    hash       uint32
    tflag      tflag
    align      uint8
    fieldalign uint8
    kind       uint8
    alg        *typeAlg
    // gcdata stores the GC type data for the garbage collector.
    // If the KindGCProg bit is set in kind, gcdata is a GC program.
    // Otherwise it is a ptrmask bitmap. See mbitmap.go for details.
    gcdata    *byte
    str       nameOff
    ptrToThis typeOff
}
```

因为 empty interface 没有声明相应的方法，`eface`直接使用了`_type`来代替`itab`。

没有运行时的判定，`eface`就可以接纳任何数据类型。

> eface 中的元数据 _type 是 Go 实现 reflect 的基础。

### 4.2 data

大部分数据类型在作为 interface 的右值时，程序的运行时会生成一份副本作为`data`的数值，除了部分类型外。

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

这部分特殊的数据类型将会直接放入到`data`这个字段里。我们来看看下面一段代码。

```
package main

type Duck struct{ x int }

func main() {
    d := Duck{}
    var eface interface{} = d

    // data is a copy
    println(&d)
    println(eface)
    println()

    i := 1
    eface = &i
    // data is not a copy
    println(&i)
    println(eface)
    println()
}

// result:
// 0xc420045f10
// (0x5a700,0xc420045f18)
//
// 0xc420045f08
// (0x52c60,0xc420045f08)
```

因为`Duck`结构体不在`isdirectiface`判定范围内，所以运行时会为`eface.data`生成一份副本。而`i`是指针类型，这个时候运行时并不会生成副本。这是 interface 的一个优化。

>  编译器会在 _type 元数据中的 kind 字段设置上 KindDirectIface 标识，这个字段主要会在 reflect 的 CanAddr 和 CanSet 两个方法中使用。

## 5 思考

回归主题，interface 设计很特别，可以用来实现『多态』、代码的模块化。

但是这毕竟有运行时的开销，interface 的滥用和声明大量的方法签名多少还是会影响到性能。

## 6 Reference

[Duke Typing](https://en.wikipedia.org/wiki/Duck_typing)

[C++ 虚函数表解析](http://coolshell.cn/articles/12165.html)

[Go Data Structures: Interfaces](https://research.swtch.com/interfaces)
