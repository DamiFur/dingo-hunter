package main

func main() {
	ch := make(chan int, 1)
	go sendAndClose(ch)
	go recvAndClose(ch)
}

func sendAndClose(ch chan int) {
	for {
		select {
		case <-ch:
		case ch <- 1:
			close(ch)
		}
	}
}

func recvAndClose(ch chan int) {
	for {
		select {
		case <-ch:
			close(ch)
		case ch <- 1:
		}
	}
}
