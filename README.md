# ING Transaction Analyzer

A tool for analyzing ING bank transactions using GPT-4.1 to extract structured information from transaction descriptions. The main interface is through an MCP server that provides programmatic access to your transaction data via the Model Context Protocol.

## Quick Start

### Installation

```bash
go install github.com/lox/ing-transaction-analyzer/cmd/ing-mcp-server@latest
```

### Configuration

1. Set up your OpenAI API key:
```bash
export OPENAI_API_KEY=your-api-key
```

2. Configure Cursor to use the MCP server by setting `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "ing-transactions": {
      "command": "ing-mcp-server"
    }
  }
}
```

### Available MCP Tools

| Tool Name | Description |
|-----------|-------------|
| `list_categories` | Lists all unique transaction categories with their counts |
| `list_transactions` | Lists transactions chronologically with optional filters |
| `search_transactions` | Searches for transactions in your history |

## Features

- Parses ING QIF transaction files
- Extracts structured information from transaction descriptions using GPT-4.1
- Stores transaction details in SQLite for efficient querying and analysis
- Progress tracking and detailed logging
- Parallel processing of transactions
- MCP server for programmatic access to transaction data

## Usage

### CLI Tool

1. Export your transactions from ING in QIF format
2. Run the analyzer:

```bash
go install github.com/lox/ing-transaction-analyzer/cmd/ing-transaction-analyzer@latest
ing-transaction-analyzer --qif-file Transactions.qif
```

### Options

- `--qif-file`: Path to the QIF file (required)
- `--data-dir`: Path to data directory (default: "./data")
- `--openai-key`: OpenAI API key (required, can also be set via OPENAI_API_KEY env var)
- `--openai-model`: OpenAI model to use for analysis (default: "gpt-4.1")
- `--concurrency`: Number of concurrent transactions to process (default: 5)
- `--verbose`: Enable verbose logging
- `--timezone`: Timezone to use for transaction dates (default: "Australia/Melbourne")

### Data Storage

Transactions are stored in a SQLite database (`transactions.db`) in your data directory. The database schema includes:

```sql
CREATE TABLE transactions (
    id TEXT PRIMARY KEY,
    date DATE NOT NULL,
    amount DECIMAL(15,2) NOT NULL,
    payee TEXT NOT NULL,
    -- Transaction details
    type TEXT NOT NULL,
    merchant TEXT NOT NULL,
    location TEXT,
    details_category TEXT,
    description TEXT,
    card_number TEXT,
    -- Foreign amount details
    foreign_amount DECIMAL(15,2),
    foreign_currency TEXT,
    -- Transfer details
    transfer_to_account TEXT,
    transfer_from_account TEXT,
    transfer_reference TEXT
)
```

The database uses two specialized virtual tables for search:

1. `transactions_fts`: A full-text search table using SQLite FTS5
2. `transactions_vec`: A vector similarity table using sqlite-vec for semantic search

### Hybrid Search

The transaction search combines both full-text search and vector similarity search using a technique called Reciprocal Rank Fusion (RRF), as described in [Alex Garcia's blog post on Hybrid Search with SQLite](https://alexgarcia.xyz/blog/2024/sqlite-vec-hybrid-search/index.html).

This hybrid approach provides several benefits:
- Full-text search catches exact and partial text matches
- Vector similarity search catches semantic similarities and related terms
- RRF combines both results, weighted to favor semantic matches
- Results include both text and vector similarity scores for transparency

Example search results show both scores:
```
Search Scores:
  Vector Score: 0.2345
  Text Score: -8.6605
  RRF Score: 0.0528
```

The RRF formula used is:
```
RRF_score = (2.0 / (k + vector_score)) + (1.0 / (k + text_score))
```
Where:
- Vector search is weighted 2x to favor semantic matches
- k = 60 (standard RRF constant)
- Lower scores indicate better matches

Indexes are created for efficient querying on:
- Payee
- Date
- Transaction type
- Merchant
- Category
- Amount

### MCP Server

The MCP server provides programmatic access to your transaction data via the Model Context Protocol (see https://modelcontextprotocol.io/introduction).

This lets you chat with the data.

#### Installation

```bash
go install github.com/lox/ing-transaction-analyzer/cmd/ing-mcp-server@latest
```

#### Configuring with Cursor

Set `.cursor/mcp.json` to:

```json
{
  "mcpServers": {
    "ing-transactions": {
      "command": "ing-mcp-server"
    }
  }
}
```

And then chat with your data, the current tools are supported:

| Tool Name | Description |
|-----------|-------------|
| `get_transactions` | Retrieves transactions from the database for a specified number of days. Requires a `days` parameter (integer) that determines how far back to look for transactions. Returns formatted transaction details including date, amount, payee, type, merchant, location, category, description, card number, foreign amount (if applicable), and transfer details (if applicable). |
| `search_transaction_freetext` | Searches transactions using free-text search. Requires a `query` parameter (string) for the search term and a `days` parameter (integer) to specify how far back to search. Supports SQLite FTS5 syntax for advanced search capabilities. Returns matching transactions with full details. |

## How it Works

1. **Initial Setup**: Import your transaction data using the CLI tool
2. **Transaction Analysis**: Each transaction's details are analyzed using GPT-4 to extract structured information
3. **Storage**: Transaction details are stored in a SQLite database for efficient querying
4. **MCP Interface**: Access and analyze your transaction data through the MCP server
5. **Results**: Query your transactions using natural language through Cursor's chat interface

## Example Output

Each transaction is stored as a JSON file with the following structure:

```json
{
  "transaction": {
    "date": "2024-03-15",
    "amount": "15.99",
    "payee": "Netflix",
    "category": "Entertainment",
    "number": "",
    "memo": ""
  },
  "details": {
    "type": "purchase",
    "merchant": "Netflix",
    "category": "Entertainment",
    "description": "Monthly subscription"
  }
}
```

## Configuration

The tool uses OpenAI's API for transaction analysis. You'll need to set your OpenAI API key:

```bash
export OPENAI_API_KEY=your-api-key
```

## Development

To build from source:

```bash
git clone https://github.com/lox/ing-transaction-analyzer
cd ing-transaction-analyzer
go build -o ing-transaction-analyzer ./cmd/ing-transaction-analyzer
go build -o ing-mcp-server ./cmd/ing-mcp-server
```

## License

MIT License (c) Lachlan Donald 2025
