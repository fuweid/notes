# Netfilter 初探

最后更新时间：`2017-03-07`

标签：`Linux` `Network` `Netfilter` `iptables`

## 前沿

Linux 内核在 2.4.x 版本中正式引入 [Netfilter](http://www.netfilter.org/) 模块，该模块负责网络数据包过滤和 [Network Address Translation](https://en.wikipedia.org/wiki/Network_address_translation)。
Netfilter 代表着一系列的 Hook ，被内核嵌入到 TCP/IP 协议栈中，数据包在穿梭协议栈时，Hook 会检查数据包，从而达到访问控制的作用。

## 工作机制

### 规则链

Netfilter 模块默认定义了五种类型的 Hook：

- PREROUTING
- INPUT
- FORWARD
- OUTPUT
- POSTROUTING

>  在 Netfilter 里，Hook 也称为 Chain，规则链

我们可以从数据包的来源和走向入手来进行分析这条五条规则链的设计。

首先，数据包按照来源可以分成 Incoming 和 Outgoing 这两种类型。

Incoming 数据包是指其他网卡发来的数据包。这类数据包可能直接奔向用户态的程序，
也有可能被内核转发到其他机器或者其他网卡上，这需要内核做路由判定。

而 Outgoing 数据包是用户态程序准备要发送的数据包。
数据包到达内核之后，内核会为它选择合适的网卡和端口，在此之后便会一层层地穿过协议栈，内核在此过程之中会做出路由判定。

> 一般情况下，客户端所使用的高端口号。在 Linux 下，我们可以通过 `cat /proc/sys/net/ipv4/ip_local_port_range` 查看系统会随机使用的端口号范围。

需要注意的是，如果这是内网和外网之间的通信，内核会使用到 NAT 技术来对地址进行转化。
对于 Incoming 数据包而言，内核路由前需要对数据包进行 Destination NAT 转化。
同理，数据包在路由之后也需要做 Source NAT 转化。

> 由于 IPv4 地址资源紧张，不能为每一个系统分配唯一的公网地址，需要通过 NAT 将私有地址转化成公网地址，才能完成对公网的访问。

根据上面的分析，可以得到以下结论：

- Incoming 数据包的目的地就在本地：PREROUTING -> INPUT
- Incoming 数据包需要转发：PREROUTING -> FORWARD -> POSTROUTING
- Outgoing 数据包：OUTPUT -> POSTROUTING

不同走向的数据包都必定会通过以上五个环节中的部分环节，只要系统管理员在五个环节中设置关卡，就可以做到系统的访问控制。

### 功能表

为了更好地管理访问控制规则，Netfilter 制定 **功能表** 来定义和区分不同功能的规则。

Netfilter 一共有五种功能表：

- raw: 当内核启动 `ip_conntrack` 模块以后，所有信息都会被追踪，raw 却是用来设置不追踪某些数据包
- mangle: 用来设置或者修改数据包的 IP 头信息
- nat: 用来设置主机的 NAT 规则，用来修改数据包的源地址和目的地址
- filter: 通常情况下，用来制定接收、转发、丢弃和拒绝数据包的规则
- security: 安全相关

> 由于 filter 和 nat 基本能满足大部分的访问控制需求，加上篇幅的原因，接下来只会介绍 filter 和 nat 这两张功能表。

不同的功能表有内置的规则链。

- filter: INPUT／OUTPUT ／FORWARD
- nat: PREROUTING ／INPUT／OUTPUT／POSTROUTING

> Linux 内核 2.6.34 开始给 nat 功能表引入了 INPUT 规则链，具体详情请查看 [Commit](http://git.kernel.org/cgit/linux/kernel/git/torvalds/linux.git/commit/?id=c68cd6cc21eb329c47ff020ff7412bf58176984e)。

不同来源的数据包的走向不同，触发的规则链也不同。


#### Incoming 数据包

```
+----------------------+                               +-------------------------------------+
| chain: PREROUTING    |                               | chain: INPUT                        |
|                      |      +==================+     |                                     |      +===============+
| table:               | -->  + Routing Decision + --> | table:                              | -->  + Local Process +
| raw -> mangle -> nat |      +=========+========+     | mangle -> filter -> security -> nat |      +===============+
+----------------------+                |              +-------------------------------------+
                                        |
                                        v
                        +------------------------------+     +---------------------+
                        | chain: FORWARD               |     | chain: POSTROUTING  |
                        |                              |     |                     |     +=========+
                        | table:                       | --> | table:              | --> + Network +
                        | mangle -> filter -> security |     | mangle -> nat       |     +=========+
                        +------------------------------+     +---------------------+
```

>  为了能使 FORWARD 生效，请确保 `cat /proc/sys/net/ipv4/ip_forward` 为1。


#### Outgoing 数据包


```
+===============+     +==================+     +----------------------+     +==================+     +--------------------+      +----------------------+     +=========+
+ Local Process + --> + Routing Decision + --> | chain: OUTPUT        | --> + Routing Decision + --> | chain: OUTPUT      | -->  | chain: POSTROUTING   | --> + Network +
+===============+     +=========+========+     |                      |     +=========+========+     |                    |      |                      |     +=========+
                                               | table:               |                              | table:             |      | table:               |
                                               | raw -> mangle -> nat |                              | filter -> security |      | mangle -> nat        |
                                               +----------------------+                              +--------------------+      +----------------------+
```

> 第一个路由判定主要是收集数据发送前的必要信息，比如所要使用的网卡、IP 地址以及端口号。

当数据包触发了协议栈中的规则链时，内核将会遍历不同的功能表（顺序如上图所示），比如 Incoming 数据包触发 PREROUTING 规则链时，内核会先执行 raw 功能表中的 PREROUTING 规则链，其次 mangle 功能表，最后才是 nat 功能表。

Netfilter 还允许系统管理员创建自己的规则链，这样可以在内置的规则链中进一步划分规则。

### 规则

功能表包含了多条规则链，而每一条规则链包含多条规则。

规则包含了 **匹配标准** 和 **具体动作**。内核会依次遍历规则链中的规则。

当数据包满足某一条规则的匹配标准时，内核将会执行规则所制定的具体动作。

比如系统管理员设置了“来自a.b.c.d的连接可以接收”这样的一条规则，其中：

- **来自a.b.c.d的连接** 指的是 匹配标准
- **接收** 是 具体动作

当具体动作只是用来 **记录日志** 或者 **标记数据包** 时，表明该动作不具有 **终结** 特性，内核还是会继续遍历规则链中剩下的规则。否则，当内核匹配上了具有终结特性动作的规则时，内核执行完具体动作之后，将停止遍历剩下的规则。

> 具体动作分为 终结 和 非终结 两种类型，其中非终结类型使用较多的一种是跳到自定义的规则链上遍历规则。
> 
> 所谓终结特性是指不会影响到数据包的命运。所以条件越苛刻的规则应该放越前面。

当规则链中没有规则，或者是数据包没有满足任何一条终结特性的规则时，内核将会采用规则链的 **策略** 来决定是否接收该数据包。

策略一共有两种：接收和丢弃。从另外一个角度看，规则链的策略体现出访问控制策略的设计：**通** 和 **堵**。

对于通策略而言，整个系统的大门是关闭着的，只有系统管理员赋予你权限才能访问。而堵则是整个系统的大门都是敞开着的，而规则将用来限制一些用户的访问。

> 自定义的规则链并不存在策略，当出现没有规则匹配或者数据包不满足规则时，将会跳回上一级，类似于函数调用栈。

## iptables

iptables 是 Netfilter 模块提供的命令接口，系统管理员可以通过它来配置各种访问控制规则。在定义规则时，可以参考以下模版。

```
# list rules in one table
# iptables -t table -nvL

# 查看 nat 功能表下规则
# iptables -t nat -nvL

# append new rule
# iptables [-t table] -A chain matchCretira -j action

# 系统管理员要限制来自 a.b.c.d IP 地址的访问
iptables -t filter -A INPUT -s a.b.c.d/n -j REJECT

# delete rule
# iptables [-t table] -D chain ruleNum

# 需要删除 filter/OUTPUT 的第二条规则
iptables -t filter -D OUTPUT 2
```

常用的具体动作有：

- filter 功能表：ACCEPT／DROP／REJECT／RETURN／LOG
- nat 功能表：SNAT／DNAT／REJECT／MASQUERADE／LOG

> `man iptables` 能提供很多信息。但说明文档始终没有更新 nat 功能表添加了 INPUT 规则链。。。



## 初体验

为了保护本地环境以及模拟多节点环境，以下实验过程都在虚拟机上运行，并在虚拟机上利用Docker 来模拟多节点的环境。

```
[root@localhost ~]# docker version | grep 'Version:' -B 1
Client:
 Version:         1.12.5
--
Server:
 Version:         1.12.5
```



整个网络模型如下图所示：

```
+---------------------------------------+
| +----------------+ +----------------+ |
| | Container #1   | | Container #2   | |
| | IP: 172.17.0.2 | | IP: 172.17.0.3 | |
| +-------------+--+ +--+-------------+ |
|               |       |               |
|               v       v               |
|           +---+-------+----+          |
|           | Bridge docker0 |          |
|           | IP: 172.17.0.1 |          |
|           +-------++-------+          |
| The               ||                  |
| Box     +--------------------+        |
+---------| Host-Only  Adapter |--------+
          | IP: 192.168.33.100 |
          +--------------------+
```

整个网络将虚拟机作为防火墙，初始状态下，不开放任何端口，将 filter 的三条内置链的策略为 DROP。

```
[root@localhost ~]# iptables -t filter -P INPUT DROP
[root@localhost ~]# iptables -t filter -P OUTPUT DROP
[root@localhost ~]# iptables -t filter -P FOPWARD DROP
```

设置完以后，你会发现你连 ping 都 ping 不通了。。。

```
$ wfu at wfu-mac in ~/workspace/docs
$ ping -c 3 192.168.33.100
PING 192.168.33.100 (192.168.33.100): 56 data bytes
Request timeout for icmp_seq 0
Request timeout for icmp_seq 1

--- 192.168.33.100 ping statistics ---
3 packets transmitted, 0 packets received, 100.0% packet loss
```

接下来的任务是能在 Mac 本地访问虚拟机里的 Docker Container，其中 Container 的启动方式如下。

```
[root@localhost ~]# docker run -it -d ubuntu-nw python -m SimpleHTTPServer 8000
3301547d70b356223688fd9e38a1925ba90028084a44775bf79422be624c486b
[root@localhost ~]# docker ps
CONTAINER ID        IMAGE               COMMAND                  CREATED             STATUS              PORTS               NAMES
3301547d70b3        ubuntu-nw           "python -m SimpleHTTP"   9 seconds ago       Up 8 seconds                            boring_borg
```

### 开放22端口

这台虚拟机默认是没有开启桌面。

```
[root@localhost ~]# stty size
25 80
```

在25 * 80这样的窗口里操作系统实在是太痛苦了。
在不启动桌面的情况，有必要通过远程登陆来改善下体验。

虚拟机上已经预先装好了ssh server，只需要开放22端口即可。

```
[root@localhost ~]# iptables -t filter -A INPUT -d 192.168.33.100 -p tcp --dport 22 -j ACCEPT
[root@localhost ~]# iptables -t filter -A OUTPUT -s 192.168.33.100 -p tcp --sport 22 -j ACCEPT
```

为什么需要两条命令？

回顾之前的内容，数据包穿过 INPUT 规则链后会被本地程序所消费。该数据包的生命周期就结束了，访问者接收到的数据包是本地程序所产生，这两者需要区分开。

第一条命令是系统管理员发给访问者的数据包的通行证，数据包到达 ssh server 之后就不复存在了，通行证也就不存在了。

ssh server 产生的数据包系统并不认识，它在穿过 OUTPUT 规则链时，如果没有通行证的话，就会被内核“吃掉”，永远都回不到访问者。

这需要两边都打通才能形成一个回路。

好了，马上登陆虚拟机。

```
$ wfu at wfu-mac in ~/workspace/docs
$ ssh root@192.168.33.100
root@192.168.33.100's password:
Last login: Fri Mar  3 18:04:55 2017 from 192.168.33.1
[root@localhost ~]# stty size
72 278
```

> 可以在必要的时候记录日志。
>
> 比如 `iptables -t filter -I INPUT 1 -p icmp -j LOG --log-prefix 'filter-input:'`，只要 [ICMP](https://en.wikipedia.org/wiki/Internet_Control_Message_Protocol) 数据包触发了 filter 功能表中的 INPUT 规则链，那么系统将会记录下该数据包的基本信息。
>
> 然后通过 `tail -f /var/log/messages` 来查看日志。

### 如何访问容器

在创建 Container 的时候，如果不制定 Network 类型，那么 Daemon 会自动将 Container 挂到 docker0 下面，并形成了一个 `172.17.0.0/16` 子网。

```
[root@localhost ~]# docker network ls
NETWORK ID          NAME                DRIVER              SCOPE
6786e7d4c36a        bridge              bridge              local
f16e611d5715        host                host                local
912ae96541e9        none                null                local
[root@localhost ~]# docker network inspect bridge
[
    {
        "Name": "bridge",
        "Id": "6786e7d4c36acbd9d359289f90bd737bfcb21e74a5e467769e45fa9f732954f2",
        "Scope": "local",
        "Driver": "bridge",
        "EnableIPv6": false,
        "IPAM": {
            "Driver": "default",
            "Options": null,
            "Config": [
                {
                    "Subnet": "172.17.0.0/16",
                    "Gateway": "172.17.0.1"
                }
            ]
        },
        "Internal": false,
        "Containers": {},
        "Options": {
            "com.docker.network.bridge.default_bridge": "true",
            "com.docker.network.bridge.enable_icc": "true",
            "com.docker.network.bridge.enable_ip_masquerade": "true",
            "com.docker.network.bridge.host_binding_ipv4": "0.0.0.0",
            "com.docker.network.bridge.name": "docker0",
            "com.docker.network.driver.mtu": "1500"
        },
        "Labels": {}
    }
]
```

而 Mac 和 虚拟机在 `192.168.33.0/24` 子网内，想要在 Mac 访问虚拟机上的 Container，需要用 Destination NAT 转发请求，所以只需要关注 `PREROUTING`／`FORWARD` 这两条规则链即可。

Docker Daemon 启动以后会自动在 Netfilter 添加访问控制规则。

```
[root@localhost ~]# iptables -t nat -nvL
Chain PREROUTING (policy ACCEPT 0 packets, 0 bytes)
 pkts bytes target     prot opt in     out     source               destination
    0     0 DOCKER     all  --  *      *       0.0.0.0/0            0.0.0.0/0            ADDRTYPE match dst-type LOCAL

Chain INPUT (policy ACCEPT 0 packets, 0 bytes)
 pkts bytes target     prot opt in     out     source               destination

Chain OUTPUT (policy ACCEPT 1 packets, 128 bytes)
 pkts bytes target     prot opt in     out     source               destination
    0     0 DOCKER     all  --  *      *       0.0.0.0/0           !127.0.0.0/8          ADDRTYPE match dst-type LOCAL

Chain POSTROUTING (policy ACCEPT 1 packets, 128 bytes)
 pkts bytes target     prot opt in     out     source               destination
    0     0 MASQUERADE  all  --  *      !docker0  172.17.0.0/16        0.0.0.0/0

Chain DOCKER (2 references)
 pkts bytes target     prot opt in     out     source               destination
    0     0 RETURN     all  --  docker0 *       0.0.0.0/0            0.0.0.0/0
```

PREROUTING 规则链只有一条规则，只要目的地址是本地地址，就跳到 DOCKER 这条自定义规则链中。
DOCKER 规则链中，只要数据包到达 docker0 网卡，就直接返回到上一层。

Mac 发来的数据包不会直接到达 docker0 网卡，而是 enp0s8 网卡。所以需要在 DOCKER 规则链中添加对来自 `192.168.33.0/24` 的 Destination NAT 规则。

> nat 功能表中 OUTPUT 规则链是用来对本地数据进行 DNAT／REDIRECT 操作，因为系统内部的通信一般情况不会穿过 PREROUTING。而在 DOCKER 规则链中添加对外部地址的 DNAT 规则并不会在 OUTPUT 规则链中被匹配。
>
> 一旦通信双方通过 nat 功能表建立连接，内核将不会使用 nat 功能表上的规则过滤该连接上的数据包。

```
[root@localhost ~]# ip addr
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN qlen 1
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host
       valid_lft forever preferred_lft forever
2: enp0s8: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc pfifo_fast state UP qlen 1000
    link/ether 08:00:27:20:9f:cb brd ff:ff:ff:ff:ff:ff
    inet 192.168.33.100/24 brd 192.168.33.255 scope global enp0s8
       valid_lft forever preferred_lft forever
    inet6 fe80::a00:27ff:fe20:9fcb/64 scope link
       valid_lft forever preferred_lft forever
3: docker0: <NO-CARRIER,BROADCAST,MULTICAST,UP> mtu 1500 qdisc noqueue state DOWN
    link/ether 02:42:c3:e1:87:ab brd ff:ff:ff:ff:ff:ff
    inet 172.17.0.1/16 scope global docker0
       valid_lft forever preferred_lft forever
```

数据包经过 PREROUTING 规则链之后，被路由到了 FORWARD 规则链。

数据包在 enp0s3 网卡与 docker0 网卡的转发，会匹配到 FORWARD 规则链的第3和第4条规则，这里不需要额外的配置。

```
[root@localhost ~]# iptables -t filter -xnvL
Chain INPUT (policy ACCEPT 9 packets, 548 bytes)
    pkts      bytes target     prot opt in     out     source               destination

Chain FORWARD (policy ACCEPT 0 packets, 0 bytes)
    pkts      bytes target     prot opt in     out     source               destination
     108    13856 DOCKER-ISOLATION  all  --  *      *       0.0.0.0/0            0.0.0.0/0
      58     3736 DOCKER     all  --  *      docker0  0.0.0.0/0            0.0.0.0/0
       0        0 ACCEPT     all  --  *      docker0  0.0.0.0/0            0.0.0.0/0            ctstate RELATED,ESTABLISHED
      50    10120 ACCEPT     all  --  docker0 !docker0  0.0.0.0/0            0.0.0.0/0
       0        0 ACCEPT     all  --  docker0 docker0  0.0.0.0/0            0.0.0.0/0

Chain OUTPUT (policy ACCEPT 7 packets, 744 bytes)
    pkts      bytes target     prot opt in     out     source               destination

Chain DOCKER (1 references)
    pkts      bytes target     prot opt in     out     source               destination

Chain DOCKER-ISOLATION (1 references)
    pkts      bytes target     prot opt in     out     source               destination
     108    13856 RETURN     all  --  *      *       0.0.0.0/0            0.0.0.0/0
```

根据上面的分析，我们只需要添加下面一条规则，便可访问该 Container。

```
[root@localhost ~]# iptables -t nat -A DOCKER -p tcp -i enp0s8 -d 192.168.33.100 --dport 80 -j DNAT --to-destination 172.17.0.2:8000
```

访问结果如下。

```
$ wfu at wfu-mac in ~/workspace/docs
$ curl 192.168.33.100
<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 3.2 Final//EN"><html>
<title>Directory listing for /</title>
<body>
<h2>Directory listing for /</h2>
<hr>
<ul>
<li><a href=".dockerenv">.dockerenv</a>
<li><a href="bin/">bin/</a>
<li><a href="boot/">boot/</a>
<li><a href="dev/">dev/</a>
<li><a href="etc/">etc/</a>
<li><a href="home/">home/</a>
<li><a href="lib/">lib/</a>
<li><a href="lib64/">lib64/</a>
<li><a href="media/">media/</a>
<li><a href="mnt/">mnt/</a>
<li><a href="opt/">opt/</a>
<li><a href="proc/">proc/</a>
<li><a href="root/">root/</a>
<li><a href="run/">run/</a>
<li><a href="sbin/">sbin/</a>
<li><a href="srv/">srv/</a>
<li><a href="sys/">sys/</a>
<li><a href="tmp/">tmp/</a>
<li><a href="usr/">usr/</a>
<li><a href="var/">var/</a>
</ul>
<hr>
</body>
</html>
```

>  `docker run -it -d ubuntu-nw -p 80:8000 python -m SimpleHTTPServer 8000` 能帮我们完成这次访问，可以观察 Docker Daemon 都为我们做了什么。
