package main

// This example test different loop and is used for checking loop SSA
// generation.

import "fmt"

func main() {

	for k := 0; false; k++ {
		fmt.Println("Loop k: ", k)
	}

	//	for i := 0; i < 3; i++ {
	for j := 0; j < 8; j++ {
		fmt.Printf("Index (%d, %d) ", 1, j)
		x := make(chan int)
		<-x
	}
	fmt.Printf("ASBCD")
	//	}
	/*
		x := []int{1, 2, 3, 4}
		for i := range x { // Range loop (safe)
			fmt.Println(i)
		}

		ch := make(chan int)
		go func(ch chan int) { ch <- 42; close(ch) }(ch)
		for v := range ch {
			fmt.Println(v)
		}

		for {
			fmt.Println("Infinite looooopppp")
		}
	*/
}

func aux() int {
	return 3
}
