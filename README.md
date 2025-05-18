# Bank Transaction Analyzer

A powerful tool for analyzing bank transactions using language models via OpenRouter to extract structured information from transaction descriptions. Provides both CLI and MCP server interfaces for flexible access to your transaction data. Supports multiple banks, with ING Australia QIF exports supported initially.

## Features

- **Transaction Analysis**
  - Multi-bank support with ING Australia QIF exports supported initially
  - Extracts structured information using models like GPT-4 via OpenRouter
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

## Terminal User Interface (TUI)

A terminal-based interface for viewing and analyzing transactions interactively.

- **Purpose:** Provides a modern, interactive terminal UI for exploring and searching your bank transactions.
- **Requirements:** Terminal with ANSI support. Go 1.24+.
- **How to Build:**
  ```bash
  go build ./cmd/bank-transaction-tui
  ```
- **How to Run:**
  ```bash
  ./cmd/bank-transaction-tui/bank-transaction-tui
  ```
- **Framework:** Built using [Bubble Tea](https://github.com/charmbracelet/bubbletea) and related Charm libraries for rich TUI experiences in Go.

## Banks Supported

- ING Australia
- American Express

## Installation

### Prerequisites

- Go 1.24 or later
- OpenRouter API key
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
2. Set up your OpenRouter API key:
   ```bash
   export OPENROUTER_API_KEY=your-api-key
   ```
3. Run the analyzer:
   ```bash
   bank-transaction-analyzer --qif-file Transactions.qif
   ```
4. Configure Cursor for MCP access:
   ```json
   {
     "mcpServers": {
       "bank-transactions": {
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
- `--openrouter-key`: OpenRouter API key (can also use env var)
- `--openrouter-model`: Model to use (default: "openai/gpt-4.1")
- `--concurrency`: Concurrent transactions to process (default: 5)
- `--verbose`: Enable verbose logging
- `--timezone`: Transaction timezone (default: "Australia/Melbourne")

#### Bank Transaction Search

```bash
bank-transaction-search --query "query" [options]
```

Options:
- `--days`: Search window in days (default: 30)
- `--limit`: Maximum results to return (default: 10)
- `--data-dir`: Data directory path
- `--vector`: Use vector search (default: false)
- `--similarity-threshold`: Minimum similarity score for vector search (default: 0.5)
- `--show-both`: Show both vector and text search results (default: false)

### MCP Server

The MCP server provides programmatic access to your transaction data through Cursor's chat interface.

Available tools:
- `search_transactions`: Search for transactions in your history
- `list_transactions`: List transactions chronologically with optional filters
- `list_categories`: List all unique transaction categories with their transaction counts

## Configuration

### Environment Variables

- `OPENROUTER_API_KEY`: Your OpenRouter API key
- `DATA_DIR`: Path to data directory
- `TZ`: Timezone for transaction dates

### Data Directory Structure

```
data/
  ├── transactions.db    # SQLite database
  ├── chromem_db         # Chroma vector database
```

## Data Storage

All transaction data is stored in a single SQLite database at `data/transactions.db`. The database uses a specialized virtual table for search:

1. `transactions_fts`: A full-text search table using SQLite FTS5

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

### Embeddings Generation

The tool supports multiple embedding providers:

1. **Local via LMStudio (Recommended)**
   - Preferred provider for local embedding generation
   - Use [LMStudio](https://lmstudio.ai/) to run a local llama.cpp server
   - Download the [tensorblock/gte-Qwen2-7B-instruct-GGUF](https://huggingface.co/tensorblock/gte-Qwen2-7B-instruct-GGUF) model (recommended for high-quality embeddings)
   - In LMStudio, add the model and start the server with embedding support enabled
   - Example LMStudio server command:
     ```bash
     lmstudio --server --model /path/to/gte-Qwen2-7B-instruct.Q4_K_M.gguf --embeddings
     ```
   - Generates high-quality embeddings suitable for semantic search
   - Runs efficiently on modern CPUs/GPUs

2. **Google Gemini API**
   - High-quality embeddings using Google's Gemini embedding model
   - Supports 3K-dimension embeddings
   - Provides better semantic understanding
   - Requires a Gemini API key

   To test Gemini embeddings:
   ```bash
   export GEMINI_API_KEY=your-gemini-api-key
   bank-transaction-analyzer embed --text "Your text to embed"
   ```

   Options:
   - `--text`: Text to generate embeddings for (required)
   - `--model-name`: Gemini model to use (default: "gemini-embedding-exp-03-07")
   - `--output-json`: Output as JSON

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
A: Categories are extracted using language models via OpenRouter to analyze transaction descriptions and merchant information.

### Q: Which embedding provider should I use?
A: If you want the highest quality embeddings, use the Gemini API provider. If you prefer to keep everything local, use the llama.cpp server provider.

### Q: Is my transaction data secure?
A: Not really, all data is stored locally in SQLite, unencrypted. No data is sent to external services except for OpenRouter API calls.

## License

MIT License (c) Lachlan Donald 2025
