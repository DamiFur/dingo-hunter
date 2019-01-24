package main

func main() {
	ch := make(chan int)
	go deadlock(ch)
	ch2 := make(chan int)
	for {
		go produce(ch2)
		<-ch2
		ch3 := make(chan int)
		ch = ch2
		ch2 = ch3
	}
}

func deadlock(ch chan int) {
	<-ch
}

func produce(ch chan int) {
	ch <- 1
}
