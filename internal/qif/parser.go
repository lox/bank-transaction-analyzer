package qif

import (
	"bufio"
	"os"
	"strings"
)

// Transaction represents a single QIF transaction
type Transaction struct {
	Date     string
	Amount   string
	Payee    string
	Category string
	Number   string
	Memo     string
}

// ParseFile reads a QIF file and returns a slice of transactions
func ParseFile(filename string) ([]Transaction, error) {
	FIELDS := map[string]string{
		"D": "date",
		"T": "amount",
		"P": "payee",
		"L": "category",
		"N": "number",
		"M": "memo",
	}

	infile, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer infile.Close()

	scanner := bufio.NewScanner(infile)
	scanner.Split(bufio.ScanLines)

	var transactions []Transaction
	current := Transaction{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) == 0 {
			continue
		}

		fieldID := string(line[0])
		if fieldID == "^" {
			if current.Date != "" {
				transactions = append(transactions, current)
				current = Transaction{}
			}
		} else if fieldName, ok := FIELDS[fieldID]; ok {
			switch fieldName {
			case "date":
				current.Date = line[1:]
			case "amount":
				current.Amount = line[1:]
			case "payee":
				current.Payee = line[1:]
			case "category":
				current.Category = line[1:]
			case "number":
				current.Number = line[1:]
			case "memo":
				current.Memo = line[1:]
			}
		}
	}

	if current.Date != "" {
		transactions = append(transactions, current)
	}

	return transactions, nil
}
