package main

import "fmt"

type Valuer interface {
	Value()
}

type MyInt int

func (i MyInt) Value() {
	fmt.Println(i)
}

func main() {
	var i MyInt = 10
	var a Valuer = i
	a.Value()
}
