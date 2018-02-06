# Goroutine Scheduler Overview

Goroutine 是 Golang 世界里的 `Lightweight Thread` 。

Golang 在语言层面支持多线程，代码可以通过  `go` 关键字来启动 Goroutine ，调用者不需要关心调用栈的大小，函数上下文等等信息就可以完成并发或者并行操作，加快了我们的开发速度。
分析 Goroutine 调度有利于了解和分析 go binary 的工作状况，所以接下来的内容将分析 `runtime` 中关于 Goroutine 调度的逻辑。

> 以下内容涉及到的代码是基于 [go1.9rc2](https://github.com/golang/go/tree/048c9cfaac) 版本。

## 1. Scheduler Structure

整个调度模型由 Goroutine/Processor/Machine 以及全局调度信息 sched 组成。

```
                   Global Runnable Queue

                          runqueue
                ----------------------------
                 | G_10 | G_11 | G_12 | ...
                ----------------------------

                                        P_0 Local Runnable Queue
                +-----+      +-----+       ---------------
                | M_3 | ---- | P_0 |  <===  | G_8 | G_9 |
                +-----+      +-----+       ---------------
                                |
                             +-----+
                             | G_3 |  Running
                             +-----+

                                        P_1 Local Runnable Queue
                +-----+      +-----+       ---------------
                | M_4 | ---- | P_1 |  <===  | G_6 | G_7 |
                +-----+      +-----+       ---------------
                                |
                             +-----+
                             | G_5 |  Running
                             +-----+
```

### 1.1 Goroutine

 Goroutine 是 Golang 世界里的 `线程` ，同样也是可调度的单元。

```
// src/runtime/runtime2.go
type g struct {
        ....
        m       *m
        sched gobuf
        goid   int64
        ....
}

type gobuf struct {
        sp   uintptr
        pc   uintptr
        ....
}
```

`runtime` 为 Goroutine 引入了类似 PID 的属性 `goid` ，使得每一个 Goroutine 都有全局唯一的 `goid` 标识。
不过官方并没有提供接口能 **直接** 访问当前 Goroutine 的 `goid`，在这种情况下我们可以通过 [汇编](https://github.com/0x04C2/gid/blob/master/gid_amd64.s#L5) 或者 [取巧](https://github.com/0x04C2/gid/blob/master/gid_test.go#L21) 的方式得到 `goid`，有些第三方 package 会利用 `goid` 做一些有趣的事情，比如 [Goroutine local storage](https://github.com/jtolds/gls) ，后面会介绍 `runtime` 是如何生成唯一的 `goid` 。

在调度过程中，`runtime` 需要 Goroutine 释放当前的计算资源，为了保证下次能恢复现场，执行的上下文现场（指令地址 和 Stack Pointer 等）将会存储在 `gobuf` 这个数据结构中。

整体来说，Goroutine 仅代表任务的内容以及上下文，并不是具体的执行单元。

### 1.2 Machine

Machine 是 OS Thread，它负责执行 Goroutine。

```
// src/runtime/runtime2.go

type m struct {
        ....
        g0      *g     // goroutine with scheduling stack
        curg    *g     // current running goroutine

        tls     [6]uintptr // thread-local storage (for x86 extern register)
        p       puintptr // attached p for executing go code (nil if not executing go code)
        ....
}
```

`runtime` 在做调度工作或者和当前 Goroutine 无关的任务时，Golang 会切换调用栈来进行相关的任务，就好像 Linux 的进程进入系统调用时会切换到内核态的调用栈一样，这么做也是为了避免影响到调度以及垃圾回收的扫描。

Machine 一般会调用 [systemstack 函数](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/stubs.go#L39) 来切换调用栈。
从名字可以看出，Golang 对外部 go code 的调用栈称之为 `user stack` ，而将运行核心 `runtime` 部分代码的调用栈称之为 `system stack`。
Machine 需要维护这两个调用栈的上下文，所以 `m` 中 `g0` 用来代表 `runtime` 内部逻辑，而 `curg` 则是我们平时写的代码，更多详情可以关注 [src/runtime/HACKING.md](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/HACKING.md#user-stacks-and-system-stacks).

因为调用栈可以来回地切换，Machine 需要知道当前运行的调用栈信息，所以 Golang 会利用 Thread Local Storage 或者指定寄存器来存储当前运行的 `g`。
`settls` 汇编代码会将 `g` 的地址放到 `m.tls` 中，这样 Machine 就可以通过 `getg` 取出当前运行的 Goroutine。

> 不同平台 `settls` 的行为有一定差别。

```
// src/runtime/sys_linux_amd64.s

// set tls base to DI
TEXT runtime·settls(SB),NOSPLIT,$32
#ifdef GOOS_android
        // Same as in sys_darwin_386.s:/ugliness, different constant.
        // DI currently holds m->tls, which must be fs:0x1d0.
        // See cgo/gcc_android_amd64.c for the derivation of the constant.
        SUBQ    $0x1d0, DI  // In android, the tls base·
#else
        ADDQ    $8, DI  // ELF wants to use -8(FS)
#endif
        MOVQ    DI, SI
        MOVQ    $0x1002, DI     // ARCH_SET_FS
        MOVQ    $158, AX        // arch_prctl
        SYSCALL
        CMPQ    AX, $0xfffffffffffff001
        JLS     2(PC)
        MOVL    $0xf1, 0xf1  // crash
        RET

// src/runtime/stubs.go

// getg returns the pointer to the current g.
// The compiler rewrites calls to this function into instructions
// that fetch the g directly (from TLS or from the dedicated register).
func getg() *g

// src/runtime/go_tls.h 

#ifdef GOARCH_amd64
#define get_tls(r)      MOVQ TLS, r
#define g(r)    0(r)(TLS*1)
#endif
```

但是 Machine 想要执行一个 Goroutine，必须要绑定 Processor。

> `runtime` 内部有些函数执行时会直接绑定 Machine，并不需要 Processor，比如 [sysmon](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L3810) 。

### 1.3 Processor

Processor 可以理解成处理器，它会维护着本地 Goroutine 队列 `runq` ，并在新的 Goroutine 入队列时分配唯一的 `goid`。

```
type p struct {
        ...
        m           muintptr   // back-link to associated m (nil if idle)

        // Cache of goroutine ids, amortizes accesses to runtime·sched.goidgen.
        goidcache    uint64
        goidcacheend uint64

        // Queue of runnable goroutines. Accessed without lock.
        runqhead uint32
        runqtail uint32
        runq     [256]guintptr
        ...
}
```

Processor 的数目代表着 `runtime` 能同时处理 Goroutine 的数目，`GOMAXPROCS` 环境变量是用来指定 Processor 的数目，默认状态会是 CPU 的个数。

也正是因为 Processor 的存在，`runtime` 并不需要做一个集中式的 Goroutine 调度，每一个 Machine 都会在 Processor 本地队列、Global Runnable Queue 或者其他 Processor 队列中找 Goroutine 执行，减少全局锁对性能的影响，后面会对此展开说明。

### 1.4 全局调度信息 sched

全局调度信息 `sched` 会记录当前 Global Runnable Queue、当前空闲的 Machine 和空闲 Processor 的数目等等。

> 后面说明这 `goidgen` 和 `nmspinning` 两个字段的作用。

```
// src/runtime/runtime2.go

var (
        ...
        sched      schedt
        ...
)

type schedt struct {
        // accessed atomically. keep at top to ensure alignment on 32-bit systems.
        goidgen  uint64

        lock mutex

        midle        muintptr // idle m's waiting for work
        nmidle       int32    // number of idle m's waiting for work
        maxmcount    int32    // maximum number of m's allowed (or die)

        pidle      puintptr // idle p's
        npidle     uint32
        nmspinning uint32 // See "Worker thread parking/unparking" comment in proc.go.

        // Global runnable queue.
        runqhead guintptr
        runqtail guintptr
        runqsize int32
        ....
}
```

## 2. Create a Goroutine

下面那段代码非常简单，在 `main` 函数中产生 Goroutine 去执行 `do()` 这个函数。

```
➜  main cat -n main.go
     1  package main
     2
     3  func do() {
     4          // nothing
     5  }
     6
     7  func main() {
     8          go do()
     9  }
```

我们编译上述代码并反汇编看看 `go` 关键字都做了什么。
可以看到源代码的第 8 行 `go do()` 编译完之后会变成 `runtime.newproc` 方法，下面我们来看看 `runtime.newproc` 都做了些什么。

```
➜  main uname -m -s
Linux x86_64
➜  main go build
➜  main go tool objdump -s "main.main" main
TEXT main.main(SB) /root/workspace/main/main.go
  main.go:7             0x450a60                64488b0c25f8ffffff      MOVQ FS:0xfffffff8, CX
  main.go:7             0x450a69                483b6110                CMPQ 0x10(CX), SP
  main.go:7             0x450a6d                7630                    JBE 0x450a9f
  main.go:7             0x450a6f                4883ec18                SUBQ $0x18, SP
  main.go:7             0x450a73                48896c2410              MOVQ BP, 0x10(SP)
  main.go:7             0x450a78                488d6c2410              LEAQ 0x10(SP), BP
  main.go:8             0x450a7d                c7042400000000          MOVL $0x0, 0(SP)
  main.go:8             0x450a84                488d05e5190200          LEAQ 0x219e5(IP), AX
  main.go:8             0x450a8b                4889442408              MOVQ AX, 0x8(SP)
  main.go:8             0x450a90                e88bb4fdff              CALL runtime.newproc(SB)  <==== I'm here.
  main.go:9             0x450a95                488b6c2410              MOVQ 0x10(SP), BP
  main.go:9             0x450a9a                4883c418                ADDQ $0x18, SP
  main.go:9             0x450a9e                c3                      RET
  main.go:7             0x450a9f                e88c7dffff              CALL runtime.morestack_noctxt(SB)
  main.go:7             0x450aa4                ebba                    JMP main.main(SB)
```

### 2.1 创建 do() 的执行上下文

平时写代码的时候会发现，Goroutine 执行完毕之后便消失了。那么 `do()` 这个函数执行完毕之后返回到哪了呢？

```
➜  main go tool objdump -s "main.do" main
TEXT main.do(SB) /root/workspace/main/main.go
  main.go:5             0x450a50                c3                      RET
```

根据 Intel 64 IA 32 开发指南上 `Chaptor 6.3 CALLING PROCEDURES USING CALL AND RET` 的说明，`RET` 会将栈顶的指令地址弹出到 `IP` 寄存器上，然后继续执行 `IP` 寄存器上的指令。
为了保证 Machine 执行完 Goroutine 之后，能够正常地完成一些清理工作，我们需要在构建 Goroutine 的执行上下文时指定 `RET` 的具体地址。

下面的代码段会将准备好的调用栈内存保存到 `newg.sched` 中，其中 `gostartcallfn` 函数会把 `do()` 函数添加到 `newg.sched.pc` ，并将 `goexit` 函数地址推入栈顶 `newg.sched.sp`。
所以 Goroutine 执行完毕之后，Machine 会跳到 `goexit` 函数中做一些清理工作。

```
// src/runtime/proc.go @ func newproc1

if narg > 0 {
        memmove(unsafe.Pointer(spArg), unsafe.Pointer(argp), uintptr(narg)
        ....
}

newg.sched.sp = sp
newg.sched.pc = funcPC(goexit) + sys.PCQuantum // +PCQuantum so that previous instruction is in same function
newg.sched.g = guintptr(unsafe.Pointer(newg))
gostartcallfn(&newg.sched, fn)
newg.gopc = callerpc
newg.startpc = fn.fn
```

> 想了解 Intel 指令的更多细节，请查看 [Intel® 64 and IA-32 Architectures Developer's Manual: Vol. 1](https://www.intel.com/content/www/us/en/architecture-and-technology/64-ia-32-architectures-software-developer-vol-1-manual.html)。

### 2.2 全局唯一的 goid

除了创建执行上下文以外，`runtime` 还会为 Goroutine 指定一个全局唯一的 id。

```
// src/runtime/proc.go

const (
        // Number of goroutine ids to grab from sched.goidgen to local per-P cache at once.
        // 16 seems to provide enough amortization, but other than that it's mostly arbitrary number.
        _GoidCacheBatch = 16
)

// src/runtime/proc.go @ func newproc1

if _p_.goidcache == _p_.goidcacheend {
        // Sched.goidgen is the last allocated id,
        // this batch must be [sched.goidgen+1, sched.goidgen+GoidCacheBatch].
        // At startup sched.goidgen=0, so main goroutine receives goid=1.
        _p_.goidcache = atomic.Xadd64(&sched.goidgen, _GoidCacheBatch)
        _p_.goidcache -= _GoidCacheBatch - 1
        _p_.goidcacheend = _p_.goidcache + _GoidCacheBatch
}
newg.goid = int64(_p_.goidcache)
_p_.goidcache++
```

全局调度信息 `sched.goidgen` 是专门用来做发号器，Processor 每次可以从发号器那拿走 `_GoidCacheBatch` 个号，然后内部采用自增的方式来发号，这样就保证了每一个 Goroutine 都可以拥有全局唯一的 `goid`。

> 从全局调度信息那里取号的时候用原子操作来保证并发操作的正确性，而内部发号时却采用非原子操作，这是因为一个 Processor 只能被一个 Machine 绑定上，所以这里 `_p_.goidcache` 自增不需要要原子操作也能保证它的正确性。

### 2.3 Local vs Global Runnable Queue

当 Goroutine 创建完毕之后，它是放在当前 Processor 的 Local Runnable Queue 还是全局队列里？

[runqput](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L4289) 这个函数会尝试把 `newg` 放到本地队列上，如果本地队列满了，它会将本地队列的前半部分和 `newg` 迁移到全局队列中。剩下的事情就等待 Machine 自己去拿任务了。

```
// src/runtime/proc.go @ func newproc1

runqput(_p_, newg, true)
```

### 2.4 小结

看到这里，一般都会有以下几个疑问：

1. main 函数是不是也是一个 Goroutine ？
2.  Machine 怎么去取 Goroutine 来执行?
3. `goexit` 做完清理工作之后就让 Machine 退出吗？还是继续使用这个 Machine?

那么就继续往下读吧~

## 3. main is a Goroutine

我们写的 `main` 函数在程序启动时，同样会以 Goroutine 身份被 Machine 执行，下面会查看 go binary 启动时都做了什么。

```
➜  main uname -m -s
Linux x86_64
➜  main go build --gcflags "-N -l"
➜  main gdb main
(gdb) info file
Symbols from "/root/workspace/main/main".
Local exec file:
        `/root/workspace/main/main', file type elf64-x86-64.
        Entry point: 0x44bb80
        0x0000000000401000 - 0x0000000000450b13 is .text
        0x0000000000451000 - 0x000000000047a6bc is .rodata
        0x000000000047a7e0 - 0x000000000047afd4 is .typelink
        0x000000000047afd8 - 0x000000000047afe0 is .itablink
        0x000000000047afe0 - 0x000000000047afe0 is .gosymtab
        0x000000000047afe0 - 0x00000000004a96c8 is .gopclntab
        0x00000000004aa000 - 0x00000000004aaa38 is .noptrdata
        0x00000000004aaa40 - 0x00000000004ab5b8 is .data
        0x00000000004ab5c0 - 0x00000000004c97e8 is .bss
        0x00000000004c9800 - 0x00000000004cbe18 is .noptrbss
        0x0000000000400fc8 - 0x0000000000401000 is .note.go.buildid
(gdb) info symbol 0x44bb80
_rt0_amd64_linux in section .text
```

入口函数是 `_rt0_amd64_linux`，需要说明的是，不同平台的入口函数名称会有所不同，全局搜索该方法之后，发现该方法会调用 `runtime.rt0_go` 汇编。

省去了大量和硬件相关的细节后，`rt0_go` 做了大量的初始化工作，`runtime.args` 读取命令行参数、`runtime.osinit` 读取 CPU 数目，`runtime.schedinit` 初始化 Processor 数目，最大的 Machine 数目等等。

除此之外，我们还看到了两个奇怪的 `g0` 和 `m0` 变量。`m0` Machine 代表着当前初始化线程，而 `g0` 代表着初始化线程 `m0` 的 `system stack`，似乎还缺一个 `p0` ？
实际上所有的 Processor 都会放到 [allp](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/runtime2.go#L722) 里。`runtime.schedinit` 会在调用 [procresize](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L3507) 时为 `m0` 分配上 `allp[0]` 。所以到目前为止，初始化线程运行模式是符合上文提到的 G/P/M 模型的。

大量的初始化工作做完之后，会调用 `runtime.newproc` 为 `mainPC` 方法生成一个 Goroutine。
虽然 [mainPC](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L106) 并不是我们平时写的那个 main 函数，但是它会调用我们写的 main 函数，所以 main 函数是会以 Goroutine 的形式运行。

有了 Goroutine 之后，那么 Machine 怎么执行呢？

```
// src/runtime/asm_amd64.s

TEXT runtime·rt0_go(SB),NOSPLIT,$0
         ...

// set the per-goroutine and per-mach "registers"
        // save m->g0 = g0
        MOVQ    CX, m_g0(AX)
        // save m0 to g0->m
        MOVQ    AX, g_m(CX)

        ...
        CALL    runtime·args(SB)
        CALL    runtime·osinit(SB)
        CALL    runtime·schedinit(SB)

        // create a new goroutine to start program
        MOVQ    $runtime·mainPC(SB), AX        // entry
        PUSHQ   AX
        PUSHQ   $0      // arg size
        CALL    runtime·newproc(SB)

        ...
        // start this M
        CALL    runtime·mstart(SB)  <=== I'm here!

        MOVL    $0xf1, 0xf1  // crash
        RET
```

## 4. Machine --- Work Stealing

在上一节查看 `rt0_go` 汇编代码的时候，发现最后一段代码 `CALL runtime.mstart(SB)` 是用来启动 Machine。

因为在 Golang 的世界里，任务的执行需要 Machine 本身自己去获取。
每个 Machine 运行前都会绑定一个 Processor，Machine 会逐步消耗完当前 Processor 队列。
为了防止某些 Machine 没有事情可做，某些 Machine 忙死，所以 `runtime` 会做了两件事：

* 当前 Processor 队列已满，Machine 会将本地队列的部分 Goroutine 迁移到 Global Runnable Queue 中;
* Machine 绑定的 Processor 没有可执行的 Goroutine 时，它会去 Global Runnable Queue、Net Network 和其他 Processor 的队列中抢任务。

这种调度模式叫做 [Work Stealing](https://en.wikipedia.org/wiki/Work_stealing)。

### 4.1 如何执行 Goroutine？

```
// src/runtime/proc.go

func mstart() {
        ...
        } else if _g_.m != &m0 {
                acquirep(_g_.m.nextp.ptr()) // 绑定 Processor
                _g_.m.nextp = 0
        }
        schedule()
}

mstart() => schedule() => execute() => xxx() => goexit()
```

`runtime.mstart` 函数会调用 [schedule](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L2195) 函数去寻找可执行的 Goroutine，查找顺序大致是:

* Local Runnable Queue
* Global Runnable Queue
* Net Network
* Other Processor's Runnable Queue

> 需找可执行的 Goroutine 的逻辑都在 [findrunnable](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L1919) 里。

找到任何一个可执行的 Goroutine 之后，会调用 [execute](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L1886) 去切换到 `g.sched` 相应的调用栈，这样 Machine 就会执行我们代码里创建 Goroutine。

执行完毕之后会 `RET` 到 `goexit`, `goexit` 会调用 [goexit0](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L2366) 进行清理工作，
然后再进入 `schedule` 模式。如果这个时候释放了当前 Machine，那么每次执行 Goroutine 都要创建新的 OS-Thread，这样的代价略大。
所以 Machine 会不断地拿任务执行，直到没有任务。
当 Machine 没有可执行的任务时，它会在 `findrunnable` 中调用 [stopm](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L1653) 进入休眠状态。

那么谁来激活这些休眠状态的 Machine ？

### 4.2 Wake Up

常见的激活时机就是新的 Goroutine 创建出来的时候。我们回头看看 `runtime.newproc` 返回前都做了什么。

```
// src/runtime/proc.go @ func newproc1

if atomic.Load(&sched.npidle) != 0 && atomic.Load(&sched.nmspinning) == 0 && runtimeInitTime != 0 {
        wakep()
}
```

当 Machine 找不到可执行的 Goroutine 时，但是还在努力地寻找可执行的 Goroutine，这段时间它属于 `spinning` 的状态。
它实在是找不到了，它才回释放当前 Processor 进入休眠状态。

`atomic.Load(&sched.npidle) != 0 && atomic.Load(&sched.nmspinning) == 0` 指的是有空闲的 Processor 而没有 `spinning` 状态的 Machine。
这个时候可能是有休眠状态的 Machine，可能是程序刚启动的时候并没有足够的 Machine。当遇到这种情况，当前 Machine 会执行 `wakep`，让程序能快速地消化 Goroutine。

> 在初始化过程中，为 `runtime.main` 函数创建的第一个 Goroutine 并不需要调用 `wakep`，所以在该判断条件里 `runtimeInitTime != 0` 会失败。
> [runtimeInitTime](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L123) 会在 `runtime.main` 函数中被赋值，表明正式开始执行任务啦。

`wakep` 首先会查看有没有空闲的 Machine，如果找到而且状态合理，那么就会激活它。如果没有找到，那么会创建一个新的 `spinning` Machine。

在 Golang 世界里，新创建的 Machine 可以认为它属于 `spinning`，因为创建 OS-Thread 有一定代价，一旦创建出来了它就要去干活。
除此之外，Golang 创建新的线程并不会直接交付任务给它，而是让它调用 `runtime.mstart` 方法自己去找活做。


```
// src/runtime/proc.go

func wakep() {
        // be conservative about spinning threads
        if !atomic.Cas(&sched.nmspinning, 0, 1) {
                return
        }
        startm(nil, true)
}

func mspinning() {
        // startm's caller incremented nmspinning. Set the new M's spinning.
        getg().m.spinning = true
}

func startm(_p_ *p, spinning bool) {
        lock(&sched.lock)
        if _p_ == nil {
                _p_ = pidleget()
                if _p_ == nil {
                        unlock(&sched.lock)
                        if spinning {
                                // The caller incremented nmspinning, but there are no idle Ps,
                                // so it's okay to just undo the increment and give up.
                                if int32(atomic.Xadd(&sched.nmspinning, -1)) < 0 {
                                        throw("startm: negative nmspinning")
                                }
                        }
                        return
                }
        }
        mp := mget()
        unlock(&sched.lock)
        if mp == nil {
                var fn func()
                if spinning {
                        // The caller incremented nmspinning, so set m.spinning in the new M.
                        fn = mspinning
                }
                newm(fn, _p_)
                return
        }
        ...
        mp.spinning = spinning
        mp.nextp.set(_p_)
        notewakeup(&mp.park)
}
```

> 在 Linux 平台上，[newm](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L1626) 会调用 [newosproc](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/os_linux.go#L144) 来产生新的 OS-Thread。

## 5. Preemptive

Machine 会在全局范围内查找 Goroutine 来执行，似乎还缺少角色去通知 Machine 释放当前 Goroutine，总不能执行完毕再切换吧。
我们知道操作系统会根据时钟周期性地触发系统中断来进行调度，Golang 是用户态的线程调度，那它怎么通知 Machine 呢？

回忆上文, 提到了有些 Machine 执行任务前它并不需要绑定 Processor，它们都做什么任务呢？

```
// src/runtime/proc.go

func main() {
        ...
        systemstack(func() {
                newm(sysmon, nil)
        })
        ...
}
```

在 `runtime.main` 函数中会启动新的 OS-Thread 去执行 [sysmon](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L3813) 函数。
该函数会以一个上帝视角去查看 Goroutine/Machine/Processor 的运行情况，并会调用 [retake](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L3940) 去让 Machine 释放正在运行的 Goroutine。

```
// src/runtime/proc.go

// forcePreemptNS is the time slice given to a G before it is
// preempted.
const forcePreemptNS = 10 * 1000 * 1000 // 10ms

func retake(now int64) uint32 {
        for i := int32(0); i < gomaxprocs; i++ {
                _p_ := allp[i]
                if _p_ == nil {
                        continue
                }
                pd := &_p_.sysmontick
                s := _p_.status

                ...
                } else if s == _Prunning {
                        // Preempt G if it's running for too long.
                        t := int64(_p_.schedtick)
                        if int64(pd.schedtick) != t {
                                pd.schedtick = uint32(t)
                                pd.schedwhen = now
                                continue
                        }
                        if pd.schedwhen+forcePreemptNS > now {
                                continue
                        }
                        preemptone(_p_)
                }
        }
        ...
}
```

Processor 在 Machine 上执行时间超过 10ms，Machine 会给调用 [preemptone](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L4024) 
给当前 Goroutine 加上标记：

```
// src/runtime/proc.go

func preemptone(_p_ *p) bool {
        ...
        gp.preempt = true

        // Every call in a go routine checks for stack overflow by
        // comparing the current stack pointer to gp->stackguard0.
        // Setting gp->stackguard0 to StackPreempt folds
        // preemption into the normal stack overflow check.
        gp.stackguard0 = stackPreempt
}
```

可以看到它并不是直接发信号给 Machine 让它立即释放，而是让 Goroutine 自己释放，那它什么时候会释放？

Golang 创建新的 Goroutine 时，都会分配有限的调用栈空间，按需进行拓展或者收缩。
所以在执行下一个函数时，它会检查调用栈是否溢出。

```
➜  main go tool objdump -s "main.main" main
TEXT main.main(SB) /root/workspace/main/main.go
  main.go:7             0x450a60                64488b0c25f8ffffff      MOVQ FS:0xfffffff8, CX
  main.go:7             0x450a69                483b6110                CMPQ 0x10(CX), SP
  main.go:7             0x450a6d                7630                    JBE 0x450a9f    <= I'm here!!
  main.go:7             0x450a6f                4883ec18                SUBQ $0x18, SP
  main.go:7             0x450a73                48896c2410              MOVQ BP, 0x10(SP)
  main.go:7             0x450a78                488d6c2410              LEAQ 0x10(SP), BP
  main.go:8             0x450a7d                c7042400000000          MOVL $0x0, 0(SP)
  main.go:8             0x450a84                488d05e5190200          LEAQ 0x219e5(IP), AX
  main.go:8             0x450a8b                4889442408              MOVQ AX, 0x8(SP)
  main.go:8             0x450a90                e88bb4fdff              CALL runtime.newproc(SB)
  main.go:9             0x450a95                488b6c2410              MOVQ 0x10(SP), BP
  main.go:9             0x450a9a                4883c418                ADDQ $0x18, SP
  main.go:9             0x450a9e                c3                      RET
  main.go:7             0x450a9f                e88c7dffff              CALL runtime.morestack_noctxt(SB)
  main.go:7             0x450aa4                ebba                    JMP main.main(SB)
```

`gp.stackguard0 = stackPreempt` 设置会让检查失败，进入 `runtime.morestack_noctxt` 函数。
它发现是因为 `runtime.retake` 造成，Machine 会保存当前 Goroutine 的执行上下文，重新进入 `runtime.schedule`。

你可能会问，如果这个 Goroutine 里面没有函数调用怎么办？请查看这个 [issues/11462](https://github.com/golang/go/issues/11462)。

一般情况下，这样的函数不是死循环，就是很快就退出了，实际开发中这种的类型函数不会太多。

## 6. 关于线程数目

Processor 的数目决定 go binary 能同时处理多少 Goroutine 的能力，感觉 Machine 的数目应该不会太多。

```
➜  scheduler cat -n main.go
     1  package main
     2
     3  import (
     4          "log"
     5          "net/http"
     6          "syscall"
     7  )
     8
     9  func main() {
    10          http.HandleFunc("/sleep", func(w http.ResponseWriter, r *http.Request) {
    11                  tspec := syscall.NsecToTimespec(1000 * 1000 * 1000)
    12                  if err := syscall.Nanosleep(&tspec, &tspec); err != nil {
    13                          panic(err)
    14                  }
    15          })
    16
    17          http.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
    18                  w.Write([]byte("hello"))
    19          })
    20
    21          log.Fatal(http.ListenAndServe(":8080", nil))
    22  }
```

Golang 提供了 `GODEBUG` 环境变量来观察当前 Goroutine/Processor/Machine 的状态。

```
➜  scheduler go build
➜  scheduler GODEBUG=schedtrace=2000 ./scheduler
SCHED 0ms: gomaxprocs=4 idleprocs=1 threads=6 spinningthreads=1 idlethreads=0 runqueue=0 [0 0 0 0]
SCHED 2008ms: gomaxprocs=4 idleprocs=4 threads=6 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 4016ms: gomaxprocs=4 idleprocs=4 threads=6 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
```

`GODEBUG=schedtrace=2000` 会开启 `schedtrace` 模式，它会让 `sysmon` 中调用 [schedtrace](https://github.com/golang/go/blob/048c9cfaacb6fe7ac342b0acd8ca8322b6c49508/src/runtime/proc.go#L4046)。

```
// src/runtime/proc.go

func schedtrace(detailed bool) {
        ...
        print("SCHED ", (now-starttime)/1e6, "ms: gomaxprocs=", gomaxprocs, " idleprocs=", sched.npidle, " threads=", sched.mcount, " spinningthreads=", sched.nmspinning, " idlethreads=", sched.nmidle, " runqueue=", sched.runqsize)
        ...
}

gomaxprocs:      当前 Processor 的数目
idleprocs:       空闲 Processor 的数目
threads:         共创建了多少个 Machine
spinningthreads: spinning 状态的 Machine
nmidle:          休眠状态的 Machine 数目
runqueue:        Global Runnable Queue 队列长度
[x, y, z..]:     每个 Processor 的 Local Runnable Queue 队列长度
```

下面我们会通过 [wrk](https://github.com/wg/wrk) 对 sleep 和 echo 这两个 endpoint 进行压力测试，并关注 Machine 的数目变化。

```
➜  scheduler GODEBUG=schedtrace=2000 ./scheduler > echo_result 2>&1 &
[1] 6015
➜  scheduler wrk -t12 -c400 -d30s http://localhost:8080/echo
Running 30s test @ http://localhost:8080/echo
  12 threads and 400 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency    51.15ms  104.96ms   1.31s    89.35%
    Req/Sec     4.97k     4.48k   20.53k    74.84%
  1780311 requests in 30.08s, 205.44MB read
Requests/sec:  59178.76
Transfer/sec:      6.83MB
➜  scheduler head -n 20 echo_result
SCHED 0ms: gomaxprocs=4 idleprocs=1 threads=6 spinningthreads=2 idlethreads=0 runqueue=0 [0 0 0 0]
SCHED 2000ms: gomaxprocs=4 idleprocs=4 threads=6 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 4005ms: gomaxprocs=4 idleprocs=4 threads=6 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 6008ms: gomaxprocs=4 idleprocs=4 threads=6 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 8014ms: gomaxprocs=4 idleprocs=0 threads=12 spinningthreads=0 idlethreads=6 runqueue=195 [20 53 6 32]
SCHED 10018ms: gomaxprocs=4 idleprocs=0 threads=12 spinningthreads=0 idlethreads=6 runqueue=272 [65 16 5 37]
SCHED 12021ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=218 [97 5 52 7]
SCHED 14028ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=41 [2 1 25 3]
SCHED 16029ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=178 [10 31 45 38]
SCHED 18033ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=144 [15 92 47 0]
SCHED 20034ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=195 [1 7 4 41]
SCHED 22035ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=159 [88 14 41 5]
SCHED 24038ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=231 [47 19 53 41]
SCHED 26046ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=6 [1 0 1 10]
SCHED 28049ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=133 [61 13 97 53]
SCHED 30049ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=220 [13 49 29 28]
SCHED 32058ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=138 [40 93 63 50]
SCHED 34062ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=266 [51 9 38 31]
SCHED 36068ms: gomaxprocs=4 idleprocs=0 threads=13 spinningthreads=0 idlethreads=7 runqueue=189 [1 3 46 14]
SCHED 38084ms: gomaxprocs=4 idleprocs=4 threads=13 spinningthreads=0 idlethreads=10 runqueue=0 [0 0 0 0]
```

测试 `localhost:8080/echo` 30s 之后，发现当前线程数目为 13。接下来再看看 `localhost:8080/sleep` 的情况。

```
➜  scheduler GODEBUG=schedtrace=1000 ./scheduler > sleep_result 2>&1 &
[1] 8284
➜  scheduler wrk -t12 -c400 -d30s http://localhost:8080/sleep
Running 30s test @ http://localhost:8080/sleep
  12 threads and 400 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency     1.01s    13.52ms   1.20s    86.57%
    Req/Sec    83.06     89.44   320.00     79.12%
  11370 requests in 30.10s, 1.26MB read
Requests/sec:    377.71
Transfer/sec:     42.79KB
➜  scheduler cat sleep_result
SCHED 0ms: gomaxprocs=4 idleprocs=1 threads=6 spinningthreads=2 idlethreads=0 runqueue=0 [0 0 0 0]
SCHED 1000ms: gomaxprocs=4 idleprocs=4 threads=6 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 2011ms: gomaxprocs=4 idleprocs=4 threads=6 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 3013ms: gomaxprocs=4 idleprocs=4 threads=282 spinningthreads=0 idlethreads=1 runqueue=0 [0 0 0 0]
SCHED 4020ms: gomaxprocs=4 idleprocs=4 threads=400 spinningthreads=0 idlethreads=1 runqueue=0 [0 0 0 0]
SCHED 5028ms: gomaxprocs=4 idleprocs=4 threads=401 spinningthreads=0 idlethreads=2 runqueue=0 [0 0 0 0]
SCHED 6037ms: gomaxprocs=4 idleprocs=4 threads=401 spinningthreads=0 idlethreads=2 runqueue=0 [0 0 0 0]
SCHED 7038ms: gomaxprocs=4 idleprocs=4 threads=402 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 8039ms: gomaxprocs=4 idleprocs=4 threads=402 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 9046ms: gomaxprocs=4 idleprocs=4 threads=402 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 10049ms: gomaxprocs=4 idleprocs=4 threads=402 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 11056ms: gomaxprocs=4 idleprocs=4 threads=402 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 12058ms: gomaxprocs=4 idleprocs=4 threads=402 spinningthreads=0 idlethreads=3 runqueue=0 [0 0 0 0]
SCHED 13058ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 14062ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 15064ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 16066ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 17068ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 18072ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 19083ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 20084ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 21086ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 22088ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 23096ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 24100ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 25100ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 26100ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 27103ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 28110ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=4 runqueue=0 [0 0 0 0]
SCHED 33131ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=396 runqueue=0 [0 0 0 0]
SCHED 34137ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=400 runqueue=0 [0 0 0 0]
SCHED 35140ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=400 runqueue=0 [0 0 0 0]
SCHED 36150ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=400 runqueue=0 [0 0 0 0]
SCHED 37155ms: gomaxprocs=4 idleprocs=4 threads=403 spinningthreads=0 idlethreads=400 runqueue=0 [0 0 0 0]
```

压力测试完毕之后，创建的线程明显比 `localhost:8080/echo` 多不少。在压测过程中采用 `gdb attach` + `thread apply all bt` 查看这些线程都在做什么:

```
...
Thread 152 (Thread 0x7f4744fb1700 (LWP 27863)):
#0  syscall.Syscall () at /usr/local/go/src/syscall/asm_linux_amd64.s:27
#1  0x000000000047151f in syscall.Nanosleep (time=0xc42119ac90,
#2  0x000000000060f042 in main.main.func1 (w=..., r=0xc4218d8900)
#3  0x00000000005e8974 in net/http.HandlerFunc.ServeHTTP (f=
#4  0x00000000005ea020 in net/http.(*ServeMux).ServeHTTP (
#5  0x00000000005eafa4 in net/http.serverHandler.ServeHTTP (sh=..., rw=...,
#6  0x00000000005e7a5d in net/http.(*conn).serve (c=0xc420263360, ctx=...)
#7  0x0000000000458e31 in runtime.goexit ()
#8  0x000000c420263360 in ?? ()
#9  0x00000000007cf100 in crypto/elliptic.p224ZeroModP63 ()
#10 0x000000c421180ec0 in ?? ()
#11 0x0000000000000000 in ?? ()
Thread 151 (Thread 0x7f47457b2700 (LWP 27862)):
#0  syscall.Syscall () at /usr/local/go/src/syscall/asm_linux_amd64.s:27
#1  0x000000000047151f in syscall.Nanosleep (time=0xc4206bcc90,
#2  0x000000000060f042 in main.main.func1 (w=..., r=0xc4218cd300)
#3  0x00000000005e8974 in net/http.HandlerFunc.ServeHTTP (f=
#4  0x00000000005ea020 in net/http.(*ServeMux).ServeHTTP (
#5  0x00000000005eafa4 in net/http.serverHandler.ServeHTTP (sh=..., rw=...,
#6  0x00000000005e7a5d in net/http.(*conn).serve (c=0xc42048afa0, ctx=...)
#7  0x0000000000458e31 in runtime.goexit ()
#8  0x000000c42048afa0 in ?? ()
#9  0x00000000007cf100 in crypto/elliptic.p224ZeroModP63 ()
#10 0x000000c4204fd080 in ?? ()
#11 0x0000000000000000 in ?? ()
...
```

> Red Hat 系列的机器可以直接使用 `pstack` 去 Dump 当前主进程内部的调用栈情况，可惜 Ubuntu 64 Bit 没有这样的包，只能自己写一个脚本去调用 `gdb` 来 Dump。

截取两个线程的调用栈信息，发现它们都在休眠状态，几乎都卡在 `/usr/local/go/src/syscall/asm_linux_amd64.s` 上。如果都阻塞了，那么它是怎么处理新来的请求？

```
// src/syscall/asm_linux_amd64.s

TEXT    ·Syscall(SB),NOSPLIT,$0-56
        CALL    runtime·entersyscall(SB)
        MOVQ    a1+8(FP), DI
        MOVQ    a2+16(FP), SI
        MOVQ    a3+24(FP), DX
        MOVQ    $0, R10
        MOVQ    $0, R8
        MOVQ    $0, R9
        MOVQ    trap+0(FP), AX	// syscall entry
        SYSCALL
        CMPQ    AX, $0xfffffffffffff001
        JLS     ok
        MOVQ    $-1, r1+32(FP)
        MOVQ    $0, r2+40(FP)
        NEGQ    AX
        MOVQ    AX, err+48(FP)
        CALL    runtime·exitsyscall(SB)
        RET
ok:
        MOVQ    AX, r1+32(FP)
        MOVQ    DX, r2+40(FP)
        MOVQ    $0, err+48(FP)
        CALL    runtime·exitsyscall(SB)
        RET
```

`Syscall` 会调用 `runtime.entersyscall` 会将当前 Processor 的状态设置为 `_Psyscall`。
当进入系统调用时间过长时，`retake` 函数在这些 `_Psyscall` Processor 的状态改为 `_Pidle`，防止长时间地占用 Processor 导致整体不工作。

进入空闲状态的 Processor 可能会被 `wakep` 函数创建出来的新进程绑定上，然而新的 Goroutine 可能还会陷入长时间的系统调用，这一来就进入恶性循环，导致 go binary 创建出大量的线程。

当然，Golang 会限制这个线程数目。

```
// src/runtime/proc.go

func checkmcount() {
        // sched lock is held
        if sched.mcount > sched.maxmcount {
                print("runtime: program exceeds ", sched.maxmcount, "-thread limit\n")
                throw("thread exhaustion")
        }
}
```

当 Machine 从内核态回来之后，会进入 `runtime.exitsyscall`。
如果执行时间很短，它会尝试地夺回之前的 Processor ；或者是尝试绑定空闲的 Processor，一旦绑定上了 Processor ，它便会继续运行当前的 Goroutine。
如果都失败了，Machine 因为没有可绑定的 Processor 而将当前的 Goroutine 放回到全局队列中，将自己进入休眠状态，等待其他 Machine 来唤醒。

一般情况下，go binary 不会创建特别多的线程，但是上线的代码还是需要做一下压测，了解一下代码的实际情况。
一旦真的创建大量的线程了，Golang 目前的版本是不会回收这些空闲的线程。
不过好在 Go10/Go11 会改进这一缺点，详情请查看 [issues/14592](https://github.com/golang/go/issues/14592)。

## 7. 总结

本文粗粒度地介绍了 Golang Goroutine Scheduler 的工作流程，并没有涉及到垃圾回收，Netpoll 以及 Channel Send/Receive 对调度的影响，希望能让读者有个大体的认识。

> `runtime.mstart` 内部的细节很多，而且很多并发操作都建立在无锁的基础上，这样能减少锁对性能的影响，感兴趣的朋友可以根据上文提到的函数一步一步地查看，应该会有不少的收获。

## 8. Reference

- [Rob Pike's 2012 Concurrency is not Parallelism](https://talks.golang.org/2012/waza.slide)
- [A Quick Guide to Go's Assembler](https://golang.org/doc/asm)
- [Scalable Go Scheduler Design Doc](https://docs.google.com/document/d/1TTj4T2JO42uD5ID9e89oa0sLKhJYD0Y_kqxDv3I3XMw/edit#heading=h.mmq8lm48qfcw)
- [Debugging performance issues in Go programs](https://software.intel.com/en-us/blogs/2014/05/10/debugging-performance-issues-in-go-programs)
