// +build go1.7

package main

import (
	"fmt"
	"os"

	"github.com/damifur/dingo-hunter/cmd"
)

func main() {
	if err := cmd.RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}
