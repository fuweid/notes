package main

import "fmt"

type Valuer interface {
	Value()
}

type MyInt int

func (v MyInt) Value() {
	fmt.Println(v)
}

func main() {
	var i MyInt = 10
	var eface interface{} = i
	var ver Valuer = i

	fmt.Println(eface)
	ver.Value()
}
