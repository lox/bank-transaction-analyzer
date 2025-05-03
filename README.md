# Bank Transaction Analyzer

A powerful tool for analyzing bank transactions using GPT-4.1 to extract structured information from transaction descriptions. Provides both CLI and MCP server interfaces for flexible access to your transaction data. Supports multiple banks, with ING Australia QIF exports supported initially.

## Features

- **Transaction Analysis**
  - Multi-bank support with ING Australia QIF exports supported initially
  - Extracts structured information using GPT-4.1
  - Categorizes transactions automatically
  - Identifies merchants and locations

- **Data Management**
  - SQLite database for efficient storage
  - Full-text and vector similarity search
  - Hybrid search using Reciprocal Rank Fusion
  - Progress tracking and detailed logging

- **Multiple Interfaces**
  - CLI tools for direct access
  - MCP server for programmatic access
  - Natural language querying through Cursor

- **Performance**
  - Parallel transaction processing
  - Optimized database indexes
  - Efficient search algorithms

## Installation

### Prerequisites

- Go 1.24 or later
- OpenAI API key
- SQLite 3

### Using Hermit

The project uses [CashApp Hermit](https://cashapp.github.io/hermit/) for dependency management. After cloning the repository, run:

```bash
cd bank-transaction-analyzer
. ./bin/activate-hermit
```

This will set up the development environment with all required dependencies.

### Available Commands

After activating Hermit, the following commands are available in your PATH:

- `bank-transaction-analyzer`: Main transaction analysis tool
- `bank-mcp-server`: MCP server for programmatic access
- `bank-transaction-search`: Search tool for transactions

## Quick Start

1. Export your transactions from your bank in QIF format (ING Australia QIF exports supported initially)
2. Set up your OpenAI API key:
   ```bash
   export OPENAI_API_KEY=your-api-key
   ```
3. Run the analyzer:
   ```bash
   bank-transaction-analyzer --qif-file Transactions.qif
   ```
4. Configure Cursor for MCP access:
   ```json
   {
     "mcpServers": {
       "ing-transactions": {
         "command": "bank-mcp-server"
       }
     }
   }
   ```

## Usage

### CLI Tools

#### Bank Transaction Analyzer

```bash
bank-transaction-analyzer --qif-file Transactions.qif [options]
```

Options:
- `--qif-file`: Path to QIF file (required)
- `--data-dir`: Data directory path (default: "./data")
- `--openai-key`: OpenAI API key (can also use env var)
- `--openai-model`: Model to use (default: "gpt-4.1")
- `--concurrency`: Concurrent transactions to process (default: 5)
- `--verbose`: Enable verbose logging
- `--timezone`: Transaction timezone (default: "Australia/Melbourne")

#### Bank Transaction Search

```bash
bank-transaction-search "query" [options]
```

Options:
- `--days`: Search window in days (default: 30)
- `--limit`: Maximum results to return (default: 10)
- `--data-dir`: Data directory path

### MCP Server

The MCP server provides programmatic access to your transaction data through Cursor's chat interface.

Available tools:
- `search_transactions`: Search for transactions in your history
- `list_transactions`: List transactions chronologically with optional filters
- `list_categories`: List all unique transaction categories with their transaction counts

## Configuration

### Environment Variables

- `OPENAI_API_KEY`: Your OpenAI API key
- `DATA_DIR`: Path to data directory
- `TZ`: Timezone for transaction dates

### Data Directory Structure

```
data/
  ├── transactions.db    # SQLite database
  ├── transactions_fts   # Full-text search index
  └── transactions_vec   # Vector similarity index
```

## Data Storage

All transaction data is stored in a single SQLite database at `data/transactions.db`. The database uses two specialized virtual tables for search:

1. `transactions_fts`: A full-text search table using SQLite FTS5
2. `transactions_vec`: A vector similarity table using sqlite-vec for semantic search

The main transactions table schema:

```sql
CREATE TABLE transactions (
    id TEXT PRIMARY KEY,
    date DATE NOT NULL,
    amount DECIMAL(15,2) NOT NULL,
    payee TEXT NOT NULL,
    type TEXT NOT NULL,
    merchant TEXT NOT NULL,
    location TEXT,
    details_category TEXT,
    description TEXT,
    card_number TEXT,
    foreign_amount DECIMAL(15,2),
    foreign_currency TEXT,
    transfer_to_account TEXT,
    transfer_from_account TEXT,
    transfer_reference TEXT
)
```

## Search Capabilities

The tool implements a hybrid search system combining:

1. **Full-text Search (FTS5)**
   - Exact and partial text matches
   - SQLite FTS5 syntax support
   - Configurable search weights

2. **Vector Similarity Search (sqlite-vec)**
   - Semantic matching using Snowflake Arctic Embed v1.5
   - Local embeddings generation via llama.cpp
   - Cosine similarity scoring

3. **Reciprocal Rank Fusion (RRF)**
   - Combines both search methods
   - Weighted towards semantic matches
   - Formula: `RRF_score = (2.0 / (k + vector_score)) + (1.0 / (k + text_score))`

### Embeddings Generation

The tool uses a local llama.cpp server for generating embeddings, running with:

```bash
build/bin/llama-server -m models/snowflake-arctic-embed-m-v1.5.d70deb40.f16.gguf --embeddings -c 768 -ngl 0
```

This configuration:
- Uses the Snowflake Arctic Embed v1.5 model
- Generates 768-dimensional embeddings
- Runs on CPU (ngl 0)
- Optimized for embedding generation

The embeddings are stored in the `transactions_vec` virtual table and used for semantic search operations.

## Development

### Building from Source

```bash
git clone https://github.com/lox/bank-transaction-analyzer
cd bank-transaction-analyzer
go build ./cmd/...
```

### Project Structure

```
.
├── cmd/                 # Command-line tools
├── internal/            # Internal packages
│   ├── analyzer/        # Transaction analysis
│   ├── bank/            # Bank-specific logic
│   ├── db/              # Database operations
│   ├── mcp/             # MCP server implementation
│   ├── qif/             # QIF file parsing
│   └── types/           # Shared types
└── data/                # Data storage
```

## FAQ

### Q: How do I update the transaction database?
A: Run the analyzer again with the new QIF file. It will update existing transactions and add new ones.

### Q: Can I use a different bank's export format?
A: Currently ING Australia QIF format is supported, with plans to add support for other banks and formats.

### Q: How are transaction categories determined?
A: Categories are extracted using GPT-4.1 analysis of transaction descriptions and merchant information.

### Q: Is my transaction data secure?
A: Not really, all data is stored locally in SQLite, unencrypted. No data is sent to external services except for OpenAI API calls.

## License

MIT License (c) Lachlan Donald 2025
