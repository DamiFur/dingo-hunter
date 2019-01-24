package main

import "fmt"

func main() {
	ch := make(chan int, 0)

	go Receive(ch)
	ch <- "Esto no debería tipar"
}

func Receive(ch chan int) {
	<-ch
}
