# ING Transaction Parser

A tool for analyzing ING bank transactions using semantic similarity and GPT-4 to identify recurring charges and similar transactions.

## Features

- Parses ING QIF transaction files
- Generates embeddings for transaction descriptions
- Groups similar transactions using semantic similarity
- Analyzes transaction groups using GPT-4 to identify recurring charges
- Persists embeddings for efficient re-analysis
- Progress tracking and detailed logging
- Automatic retry with exponential backoff for API calls

## Installation

```bash
go install github.com/lox/ing-transaction-parser/cmd/ing-transaction-parser@latest
```

## Usage

1. Export your transactions from ING in QIF format
2. Run the parser:

```bash
ing-transaction-parser Transactions.qif
```

### Options

- `-file`: Path to the QIF file (required)
- `-verbose`: Enable verbose logging
- `-concurrency`: Number of concurrent embedding generations (default: 5)
- `-embedding-model`: OpenAI embedding model to use (default: "text-embedding-3-small")
- `-analysis-model`: OpenAI model to use for analysis (default: "gpt-4")

## How it Works

1. **Transaction Parsing**: The tool reads your QIF file and extracts transaction data
2. **Embedding Generation**: Each transaction's payee is converted into an embedding vector using OpenAI's embedding API
3. **Similarity Grouping**: Transactions with similar embeddings are grouped together
4. **Analysis**: Each group is analyzed by GPT-4 to identify recurring charges and patterns
5. **Results**: The analysis results are displayed, showing:
   - Groups of similar transactions
   - Likelihood of being recurring charges
   - Analysis of transaction patterns

## Example Output

```
Group 1 (Similarity: 0.92):
- Netflix Subscription - $15.99
- Netflix Subscription - $15.99
- Netflix Subscription - $15.99

Analysis: These appear to be monthly Netflix subscription charges. The consistent amount and timing suggest this is a recurring charge.

Group 2 (Similarity: 0.85):
- Amazon.com - $45.67
- Amazon.com - $32.99
- Amazon.com - $89.12

Analysis: These are Amazon purchases. While they're from the same merchant, the varying amounts suggest these are individual purchases rather than recurring charges.
```

## Configuration

The tool uses OpenAI's API for both embedding generation and analysis. You'll need to set your OpenAI API key:

```bash
export OPENAI_API_KEY=your-api-key
```

## Development

To build from source:

```bash
git clone https://github.com/lox/ing-transaction-parser
cd ing-transaction-parser
go build -o ing-transaction-parser ./cmd/ing-transaction-parser
```

## License

MIT License (c) Lachlan Donald 2025
