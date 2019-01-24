package main

import "fmt"

func main() {
	ch := make(chan int, 1)

	ch <- 1
	_ = <-ch

	close(ch)

	_ = <-ch
	ch <- 0
	fmt.Println("I should have a not-safe operation on this last send")
}
