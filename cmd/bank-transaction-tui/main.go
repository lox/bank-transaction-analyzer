package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/commands"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/embeddings"
	"github.com/lox/bank-transaction-analyzer/internal/search"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

const itemsPerPage = 15

type keyMap struct {
	Up          key.Binding
	Down        key.Binding
	PageDown    key.Binding
	PageUp      key.Binding
	Quit        key.Binding
	OrderToggle key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:          key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:        key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		PageDown:    key.NewBinding(key.WithKeys("pgdown", "ctrl+f"), key.WithHelp("pgdn/ctrl+f", "page down")),
		PageUp:      key.NewBinding(key.WithKeys("pgup", "ctrl+b"), key.WithHelp("pgup/ctrl+b", "page up")),
		Quit:        key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		OrderToggle: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "toggle order")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.PageUp, k.PageDown, k.Quit, k.OrderToggle}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Quit, k.OrderToggle},
	}
}

type model struct {
	transactions      []types.TransactionWithDetails
	totalTransactions int
	cursor            int
	width             int
	height            int
	quitting          bool
	err               error
	ready             bool
	db                *db.DB
	help              help.Model
	keys              keyMap

	// Search state
	searchActive           bool
	searchQuery            string
	searchInput            textinput.Model
	searchResults          []types.TransactionWithDetails
	searchTotal            int
	searchOrderByRelevance bool

	embeddingProvider embeddings.EmbeddingProvider
	vectorStorage     embeddings.VectorStorage
	logger            *log.Logger
}

type transactionDataMsg struct {
	transactions []types.TransactionWithDetails
	totalCount   int
}

type errorMsg struct{ err error }

func (e errorMsg) Error() string { return e.err.Error() }

func initialModel(dbConn *db.DB, embeddingProvider embeddings.EmbeddingProvider, vectorStorage embeddings.VectorStorage, logger *log.Logger) model {
	helpUI := help.New()
	ti := textinput.New()
	ti.Placeholder = "Search..."
	ti.CharLimit = 156
	ti.Width = 40
	return model{
		db:                dbConn,
		help:              helpUI,
		keys:              newKeyMap(),
		cursor:            0,
		width:             80,
		height:            24,
		ready:             false,
		searchActive:      false,
		searchInput:       ti,
		embeddingProvider: embeddingProvider,
		vectorStorage:     vectorStorage,
		logger:            logger,
	}
}

func (m model) Init() tea.Cmd {
	return m.fetchTransactionsCmd()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.searchInput.Width = m.width - 2
	case tea.KeyMsg:
		if m.searchActive {
			if msg.String() == "enter" {
				query := m.searchInput.Value()
				if query == "" {
					m.searchActive = false
					m.searchQuery = ""
					m.cursor = 0
					return m, m.fetchTransactionsCmd()
				}
				m.searchQuery = query
				m.searchActive = false
				return m, m.fetchSearchCmd(query)
			}
			if msg.String() == "esc" {
				m.searchActive = false
				m.searchQuery = ""
				m.cursor = 0
				return m, m.fetchTransactionsCmd()
			}
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			return m, tea.Quit
		case msg.String() == "/":
			m.searchActive = true
			m.searchInput.SetValue("")
			m.searchInput.Focus()
			return m, nil
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < m.currentTransactionsCount()-1 {
				m.cursor++
			}
		case key.Matches(msg, m.keys.PageDown):
			if m.currentTransactionsCount() == 0 {
				break
			}
			m.cursor += itemsPerPage
			if m.cursor > m.currentTransactionsCount()-1 {
				m.cursor = m.currentTransactionsCount() - 1
			}
		case key.Matches(msg, m.keys.PageUp):
			if m.currentTransactionsCount() == 0 {
				break
			}
			m.cursor -= itemsPerPage
			if m.cursor < 0 {
				m.cursor = 0
			}
		case msg.String() == "o":
			if m.searchQuery != "" {
				m.searchOrderByRelevance = !m.searchOrderByRelevance
				return m, m.fetchSearchCmd(m.searchQuery)
			}
		}
	case transactionDataMsg:
		m.ready = true
		m.err = nil
		m.transactions = msg.transactions
		m.totalTransactions = msg.totalCount
		m.cursor = 0
		m.searchResults = nil
		m.searchTotal = 0
	case searchDataMsg:
		m.ready = true
		m.err = nil
		m.searchResults = msg.transactions
		m.searchTotal = msg.totalCount
		m.cursor = 0
	case errorMsg:
		m.err = msg.err
	}
	return m, nil
}

func (m model) currentTransactions() []types.TransactionWithDetails {
	if m.searchQuery != "" {
		return m.searchResults
	}
	return m.transactions
}

func (m model) currentTransactionsCount() int {
	return len(m.currentTransactions())
}

type searchDataMsg struct {
	transactions []types.TransactionWithDetails
	totalCount   int
}

func (m model) fetchSearchCmd(query string) tea.Cmd {
	return func() tea.Msg {
		if m.db == nil || m.embeddingProvider == nil || m.vectorStorage == nil {
			return errorMsg{fmt.Errorf("database or embedding components not initialized")}
		}
		ctx := context.Background()

		orderByOption := search.OrderByRelevance()
		if !m.searchOrderByRelevance {
			orderByOption = search.OrderByDate()
		}
		results, err := search.HybridSearch(
			ctx,
			m.logger,
			m.db,
			m.embeddingProvider,
			m.vectorStorage,
			query,
			orderByOption,
		)
		if err != nil {
			return errorMsg{fmt.Errorf("failed to search transactions: %w", err)}
		}
		var txs []types.TransactionWithDetails
		for _, r := range results.Results {
			txs = append(txs, r.TransactionWithDetails)
		}
		return searchDataMsg{transactions: txs, totalCount: results.TotalCount}
	}
}

func (m model) fetchTransactionsCmd() tea.Cmd {
	return func() tea.Msg {
		if m.db == nil {
			return errorMsg{fmt.Errorf("database not initialized")}
		}
		ctx := context.Background()
		transactions, err := m.db.GetTransactions(ctx)
		if err != nil {
			return errorMsg{fmt.Errorf("failed to get transactions: %w", err)}
		}
		count := len(transactions)
		return transactionDataMsg{transactions: transactions, totalCount: count}
	}
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	if m.err != nil {
		return fmt.Sprintf("\nAn error occurred: %v\n\nPress q to quit.", m.err)
	}
	if !m.ready {
		return "\nLoading transactions...\n\nPress q to quit."
	}

	var txs []types.TransactionWithDetails
	var status string
	if m.searchQuery != "" {
		txs = m.searchResults
		order := "relevance"
		if !m.searchOrderByRelevance {
			order = "date"
		}
		if len(m.searchResults) == 0 {
			status = fmt.Sprintf("Search: \"%s\" — No results (ordered by %s)", m.searchQuery, order)
		} else {
			plural := "s"
			if len(m.searchResults) == 1 {
				plural = ""
			}
			status = fmt.Sprintf("Search: \"%s\" — %d result%s (ordered by %s)", m.searchQuery, len(m.searchResults), plural, order)
		}
	} else {
		txs = m.transactions
		status = fmt.Sprintf("Transaction %d of %d", m.cursor+1, m.totalTransactions)
	}

	// Determine the window of transactions to display
	start := m.cursor - itemsPerPage/2
	if start < 0 {
		start = 0
	}
	end := start + itemsPerPage
	if end > len(txs) {
		end = len(txs)
		start = end - itemsPerPage
		if start < 0 {
			start = 0
		}
	}

	var b strings.Builder
	if len(txs) == 0 {
		b.WriteString("No transactions found.")
	} else {
		for i := start; i < end; i++ {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			t := txs[i]
			payee := t.Payee
			maxPayeeLen := m.width - 20
			if maxPayeeLen < 10 {
				maxPayeeLen = 10
			}
			if len(payee) > maxPayeeLen {
				payee = payee[:maxPayeeLen-3] + "..."
			}
			b.WriteString(fmt.Sprintf("%s%s | %10s | %s\n", cursor, t.Date, t.Amount, payee))
		}
	}

	var searchBar string
	if m.searchActive {
		searchBar = "/" + m.searchInput.View()
	}

	help := m.help.View(struct {
		keyMap
	}{
		keyMap: keyMap{
			Up:          m.keys.Up,
			Down:        m.keys.Down,
			PageUp:      m.keys.PageUp,
			PageDown:    m.keys.PageDown,
			OrderToggle: m.keys.OrderToggle,
			Quit:        m.keys.Quit,
		},
	})

	lines := []string{status, "", b.String()}
	if m.searchActive {
		lines = append(lines, searchBar)
	}
	lines = append(lines, help)
	output := strings.Join(lines, "\n")

	lineCount := strings.Count(output, "\n") + 1
	if lineCount < m.height {
		output += strings.Repeat("\n", m.height-lineCount)
	}

	return output
}

func main() {
	type CLI struct {
		commands.CommonConfig
		commands.EmbeddingConfig
	}

	var cli CLI
	ctxKong := kong.Parse(&cli,
		kong.Name("bank-transaction-tui"),
		kong.Description("A TUI for viewing and analyzing bank transactions."),
		kong.UsageOnError(),
	)
	_ = ctxKong

	logger := log.NewWithOptions(os.Stderr, log.Options{
		Level:  log.InfoLevel,
		Prefix: "tui",
	})

	parsedLevel, err := log.ParseLevel(cli.LogLevel)
	if err != nil {
		logger.Warn("Invalid log level specified, defaulting to info", "error", err, "specifiedLevel", cli.LogLevel)
		parsedLevel = log.InfoLevel
	}
	logger.SetLevel(parsedLevel)

	loc, err := time.LoadLocation(cli.Timezone)
	if err != nil {
		logger.Fatal("Failed to load timezone", "error", err, "timezone", cli.Timezone)
	}

	if err := os.MkdirAll(cli.DataDir, 0750); err != nil {
		logger.Fatal("Failed to create data directory", "error", err, "datadir", cli.DataDir)
	}

	logger.Info("Loading database", "data_dir", cli.DataDir, "timezone", cli.Timezone)

	dbConn, err := db.New(cli.DataDir, logger, loc)
	if err != nil {
		logger.Fatal("Failed to initialize database", "error", err)
	}
	defer func() {
		if err := dbConn.Close(); err != nil {
			logger.Error("Failed to close database", "error", err)
		}
	}()

	// Initialize embedding provider and vector storage
	ctx := context.Background()
	embeddingProvider, err := commands.SetupEmbeddingProvider(ctx, cli.EmbeddingConfig, logger)
	if err != nil {
		logger.Fatal("Failed to initialize embedding provider", "error", err)
	}
	vectorStorage, err := commands.SetupVectorStorage(ctx, cli.DataDir, embeddingProvider, logger)
	if err != nil {
		logger.Fatal("Failed to initialize vector storage", "error", err)
	}
	defer commands.CloseEmbeddingProvider(embeddingProvider, logger)

	p := tea.NewProgram(initialModel(dbConn, embeddingProvider, vectorStorage, logger), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		logger.Fatal("Error running TUI", "error", err)
		os.Exit(1)
	}
}
