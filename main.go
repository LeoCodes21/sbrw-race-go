package main

import (
	"github.com/leocodes21/sbrw-race-go/internal"
	"fmt"
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
