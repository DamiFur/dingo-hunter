package main

import "fmt"

func Producer(ch chan int) {
	ch <- 1
}

func Consumer(ch chan int) {
	ch <- 1
}

func main() {
	ch := make(chan int)

	go createProducerConsumersAndNotify(ch, 0)

	var x int
	for {
		x = <-ch
		fmt.Println("Amount of producer-consumers launched: ", x)
	}
}

func createProducerConsumersAndNotify(ch chan int, acum int) {
	for {
		newChan := make(chan int)
		go Producer(newChan)
		go Consumer(newChan)
		acum += 1
		ch <- acum
	}
}
