package main

import (
	"fmt"
	"time"
)

type ValueUpdater struct {
	mutex  chan int
	global int
}

func (vu *ValueUpdater) UpdateGlobal(i int) {
	<-vu.mutex
	vu.global += i
	fmt.Println(vu.global)
	vu.mutex <- 1
}

func main() {
	mutex := make(chan int, 1)
	vu := ValueUpdater{}
	vu.mutex = mutex
	go update(&vu)
	go update(&vu)
	vu.mutex <- 1
	time.Sleep(1 * time.Second)
}

func update(vu *ValueUpdater) {
	vu.UpdateGlobal(1)
}
