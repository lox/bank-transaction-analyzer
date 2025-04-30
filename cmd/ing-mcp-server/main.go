package main

import (
	"github.com/lox/ing-transaction-analyzer/internal/mcp"
)

func main() {
	s := mcp.New()
	if err := s.Run(); err != nil {
		panic(err)
	}
}
