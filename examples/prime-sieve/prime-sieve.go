package main

func Generate(ch chan<- int) {
	for i := 2; ; i++ {
		ch <- i
	} // Send sequence 2,3...
}

func Filter(in <-chan int, out chan<- int, prime int) {
	for {
		i := <-in // Receive value from ’in’.
		if i%prime != 0 {
			out <- i
		} // Fwd ’i’ if factor.
	}
}

func main() {
	ch := make(chan int) // Create new channel.
	go Generate(ch)      // Spawn generator.
	for i := 0; ; i++ {
		prime := <-ch
		ch1 := make(chan int)
		go Filter(ch, ch1, prime) // Chain filter.
		ch = ch1
	}
}
