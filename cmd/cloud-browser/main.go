package main

import (
	"cloudbrowser/internal/manager"
	"os"
)

func main() { os.Exit(manager.Run(os.Args[1:])) }
