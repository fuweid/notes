
# 让你的 shell 脚本变得可控

最后更新时间: `2017-03-20`

标签: `Linux` `Unix` `Shell` `BASH`


刚开始接触 shell 脚本的时候，最痛苦的地方在于出了问题，却不容易定位问题。

shell 脚本遇到错误，“大部分” 情况下都会继续执行剩下的命令，最后返回 Zero [Exit Code](https://en.wikipedia.org/wiki/Exit_status)  并不代表着结果正确。

这让人很难发现问题，它不像其他脚本语言，遇到 `语法错误` 和 `typo` 等错误时便会立即退出。

如果想要写出容易维护、容易 debug 的 shell 脚本，我们就需要让 shell 脚本变得可控。

## set -e

默认情况下，shell 脚本遇到错误并不会立即退出，它还是会继续执行剩下的命令。

```
[root@localhost ~]# cat example
#!/usr/bin/env bash
# set -e

sayhi # this command is not available.
echo "sayhi"
[root@localhost ~]# ./example
./example: line 4: sayhi: command not found
sayhi
```

我们知道 Linux/Unix 用户等于系统的时候，内核会加载 `.bashrc` 或者 `.bash_profile` 里的配置。

> 不同 shell 版本会使用不同的 rc/profile 文件，比如 zsh 版本的 rc 文件名是 .zshrc。

简单设想下，假如 shell 脚本遇到错误就退出，那么只要这些文件里有 typo 等错误，该用户就永远登陆不了系统。

> 在此，并没有考究默认行为的设计缘由，只是想表达 shell 脚本默认行为会让脚本变得不可控。

`set -e` 能会让 shell 脚本遇到 Non-Zero Exit Code 时，会立即停止执行。

```
[root@localhost ~]# cat example
#!/usr/bin/env bash
set -e

sayhi # this command is not available.
echo "sayhi"
[root@localhost ~]# ./example
./example: line 4: sayhi: command not found
```

## set -u

初始化后再使用变量，这是好的编程习惯。

但在默认情况下，shell 脚本使用未初始化的变量并不会报错。

```
[root@localhost ~]# cat example
#!/usr/bin/env bash
# set -u

echo "Hi, ${1}"
[root@localhost ~]# ./example
Hi,
[root@localhost ~]# echo $?
0
```

若脚本设置 `set -u` ，一旦使用没有初始化的变量或者 `positional parameter` 时，脚本将立即返回 1 Exit Code。

```
[root@localhost ~]# cat example
#!/usr/bin/env bash
set -u

echo "Hi, ${1}"
[root@localhost ~]# ./example
./example: line 4: 1: unbound variable
[root@localhost ~]# echo $?
1
```

> 需要说明的是，对于预定义的 `$@`, `$*` 等这些变量，是可以正常使用。

为了避免使用未初始化的变量，常使用 `${VAR:-DEFAULT}` 来设置默认值。

```
#!/usr/bin/env bash
set -u

# ${VAR:-DEFAULT} evals to DEFAULT if VAR undefined.
foo=${nonexisting:-ping}

echo "${foo}" # => ping

bar="pong"

foo=${bar:-ping}

echo "${foo}" # => pong

# DEFAULT can be empty
empty=${nonexisting:-}

echo "${empty}" # => ''
```

## set -o pipefail

在默认情况下，`pipeline` 会采用最后一个命令的 Exit Code 作为最终返回的 Exit Code。

```
[root@localhost ~]# cat example
#!/usr/bin/env bash
# set -o pipefail

grep string /non-existing-file | sort
[root@localhost ~]# ./example
grep: /non-existing-file: No such file or directory
[root@localhost ~]# echo $?
0
```
明明报错了，为什么还会返回 Zero Exit Code?

`grep` 一个并不存在的文件会返回 2 Exit Code。`grep` 不仅会输出错误信息到 `STDERR` 上，还会输出空的字符串到 `STDOUT`。对于 `sort` 命令而言，空字符串是合法的输入，所以最后命令返回 Zero Exit Code。

这样错误信息并不能很好地帮助我们改善脚本，返回的 Exit Code 应该要尽可能地反映错误现场。

和前面两个设置一样，`set -o pipefail` 会让 shell 脚本在 `pipeline` 过程遇到错误便立即返回相应错误的 Exit Code。

```
[root@localhost ~]# cat example
#!/usr/bin/env bash
set -o pipefail

grep string /non-existing-file | sort
[root@localhost ~]# ./example
grep: /non-existing-file: No such file or directory
[root@localhost ~]# echo $?
2
```

## non-zero exit code is expected

这三个配置太过于苛刻，某些情况下还需要放宽这些限制：当程序可以接受 non-zero exit code 时。

这里有两种常用的方式去放宽限制：

### set +

这里有一个脚本是用来产生长度为 64 的随机字符串：

```
root@localhost ~]# cat example
#!/usr/bin/env bash
set -euo pipefail

str=$(cat /dev/urandom | tr -dc '0-9A-Za-z' | head -c 64)

echo "${str}"

[root@localhost ~]# ./example
[root@localhost ~]# echo $?
141
```

该脚本有一个问题，就是 `head` 命令在获取到第 64 个字节之后，会关闭 `STDIN`，但是 `pipe` 还在不断地输出，导致内核不得不抛出 `SIGPIPE` 来终止命令。

因为设置 `set -o pipefail` 了 ，整个脚本因为 `SIGPIPE` 会退出。

假设该脚本剩下命令还很多，不能整体去掉 `pipefail` ，那么我们就局部放弃这个限制好了。

```
[root@localhost ~]# cat example
#!/usr/bin/env bash
set -euo pipefail

set +o pipefail
str=$(cat /dev/urandom | tr -dc '0-9A-Za-z' | head -c 64)
set -o pipefail

echo "${str}"

[root@localhost ~]# ./example
pvScFHDZrdjlI091rQbruyEPM9e6iTN59IyzaKcCJwiCxYmiSNRmkFOfp0YuXi1C
[root@localhost ~]# echo $?
0
```

同理，只要设置上 `set +e` 或者 `set +u` 时，就会放宽相应的限制。

记得 **有借有还，再借不难** 就好了。

### 短路运算

现在有一个脚本，该脚本用来统计文件 `file` 中有多少行是包含了 `string` 这个字符串。

```
root@localhost ~]# cat example
#!/usr/bin/env bash
set -euo pipefail

count=$(grep -c string ./file)

echo "${count}"

[root@localhost ~]# ./example
[root@localhost ~]# echo $?
1
[root@localhost ~]# cat ./file
example
```

因为文件 `file` 中并不包含 `string` 这一字符串，所以 `grep` 返回 1 Exit Code。

假设遇到没有匹配上的文件，该脚本应该显示零，而不是错误。

`$(cmd || true)` 短路运算会让该命令永远都正常执行。

```
[root@localhost ~]# cat example
#!/usr/bin/env bash
set -euo pipefail

count=$(grep -c string ./file || true)

echo "${count}"

[root@localhost ~]# ./example
0
[root@localhost ~]# echo $?
0
```


## 思考

shell 脚本能帮助我们轻松地完成自动化的任务，这是它的优势。

但是劣势也比较明显，就是 shell 脚本的返回值。我们来看看下面的一个例子。

相对于 `if/else`, 短路运算可以让代码变得简洁。

但是一旦最终的判断结果为否，那么该短路运算将会返回 Non-Zero Exit Code。

假如有一个脚本的最后一条命令是短路运算。

```
[root@localhost ~]# cat ./echo_filename
#!/usr/bin/env bash
set -euo pipefail

file=${1:-}

[[ -f "${file}" ]] && echo "File: ${file}"
[root@localhost ~]# ./echo_filename
[root@localhost ~]# echo $?
1
```

如果没有传参数，那么短路运算将会返回 1 Exit Code，这个结果也将作为整个脚本的返回结果。

> 需要说明的是，虽然短路运算返回的 Non-Zero Exit Code，但 `set -e` 不会因为它而退出。

然后我们再看看使用 `if/else` 的结果。

```
[root@localhost ~]# cat echo_filename
#!/usr/bin/env bash
set -euo pipefail

file=${1:-}

if [[ -f "${file}" ]]; then
    echo "File: ${file}"
fi
[root@localhost ~]# ./echo_filename
[root@localhost ~]# echo $?
0
```

从逻辑上来分析，即使不传参数，呈现的应该是空字符串，并返回 Zero Exit Code。

`if/else` 语句相对于短路运算药合理。

我们别小看这一区别，如果这里有脚本调用 `echo_filename`，那么使用短路运算将会导致调用该脚本的脚本停止工作。

归根结底，是因为 shell 脚本并不像其他语言那样支持返回多种数据类型，它只能返回数字的 Exit Code。

这就代表着脚本的程序设计必须要考虑返回正确的 Exit Code，这样 `set -euo pipefail` 才能让脚本变得更加可控。

> 关于 `set` 更多的内容，请前往 [Link](https://www.gnu.org/software/bash/manual/bashref.html#The-Set-Builtin) 。



