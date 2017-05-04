package main

import "fmt"

type Valuer interface {
	Value()
}

type MyInt int

func (v MyInt) Value() {
	fmt.Println(v)
}

func example(v interface{}) {
	if vv, ok := v.(Valuer); ok {
		vv.Value()
	}
}

func main() {
	var i MyInt = 10
	example(i)
}
