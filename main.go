package main

import (
	"fmt"
	"github.com/LeoCodes21/sbrw-race-go/internal"
	"os"
	"os/signal"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Error caught: ", r)
		}
	}()

	internal.Start(":9998")
	fmt.Println("Server running on port 9998")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
