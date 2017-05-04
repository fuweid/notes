package main

import "fmt"

type Abser interface {
	Abs() float64
}

type MyFloat float64

func (f MyFloat) Abs() float64 {
	if f < 0 {
		return float64(-f)
	} else {
		return float64(f)
	}
}

func main() {
	var a Abser = MyFloat(-1.0)
	fmt.Println(a.Abs())
}
