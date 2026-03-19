package main

import (
	"fmt"
	"os"

	"git-frontend/internal/app"
)

const version = "0.0.1"

func main() {
	// Create and run the app
	a := app.New()
	
	if err := a.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running app: %v\n", err)
		os.Exit(1)
	}
}
