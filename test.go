package main

func main() {
	ch := make(chan int)
	dummy := 1
	go recv(ch, dummy)
	for {
		ch <- 1
	}
}

func recv(ch chan int, dummy int) {
	for {
		if dummy == 1 {
			<-ch
		}
	}
}
