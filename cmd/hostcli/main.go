// Package main provides the entry point for the AirFi Host CLI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
)

var (
	version    = "0.1.0"
	apiURL     string
	httpClient = &http.Client{Timeout: 10 * time.Second}
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "airfi-host",
		Short: "AirFi Host CLI - WiFi Provider Terminal",
		Long: `AirFi Host CLI is a terminal tool for WiFi providers to:
- Display session QR codes for guests
- Monitor active sessions and payments
- Manage channel settlements`,
		Version: version,
	}

	// Global flags
	rootCmd.PersistentFlags().StringVar(&apiURL, "api", "http://localhost:8080", "Backend API URL")

	// Commands
	rootCmd.AddCommand(
		newDashboardCommand(),
		newQRCommand(),
		newSessionsCommand(),
		newSettleCommand(),
		newStatusCommand(),
		newWalletCommand(),
		newTokenCommand(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newDashboardCommand creates the main dashboard command.
func newDashboardCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Launch the host dashboard (QR + wallet + sessions)",
		Long:  "Displays an interactive dashboard with QR code, wallet info, and live session monitoring",
		Run: func(cmd *cobra.Command, args []string) {
			runDashboard()
		},
	}
}

// newQRCommand creates the QR code display command.
func newQRCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "qr",
		Short: "Display the WiFi access QR code",
		Long:  "Generates and displays a QR code for guests to scan and access WiFi",
		Run: func(cmd *cobra.Command, args []string) {
			displayQRCode()
		},
	}
}

// newSessionsCommand creates the sessions list command.
func newSessionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List active sessions",
		Long:  "Shows all currently active WiFi sessions with their status",
		Run: func(cmd *cobra.Command, args []string) {
			listSessions()
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "watch",
		Short: "Watch sessions in real-time",
		Run: func(cmd *cobra.Command, args []string) {
			watchSessions()
		},
	})

	return cmd
}

// newSettleCommand creates the settle command.
func newSettleCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "settle [channel-id]",
		Short: "Settle a payment channel",
		Long:  "Initiates settlement of a payment channel to receive funds",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			settleChannel(args[0])
		},
	}
}

// newStatusCommand creates the status command.
func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show system status",
		Long:  "Displays the current status of the AirFi backend",
		Run: func(cmd *cobra.Command, args []string) {
			showStatus()
		},
	}
}

// newWalletCommand creates the wallet command.
func newWalletCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "wallet",
		Short: "Show wallet info",
		Long:  "Displays wallet address and balance",
		Run: func(cmd *cobra.Command, args []string) {
			showWallet()
		},
	}
}

// newTokenCommand creates the token command for getting JWT.
func newTokenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "token [session-id]",
		Short: "Get JWT access token for a session",
		Long:  "Retrieves the JWT access token for WiFi authentication. Only works for active sessions.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			getSessionToken(args[0])
		},
	}
}

func runDashboard() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		cancel()
	}()

	// Track known sessions to detect changes
	knownSessions := make(map[string]*Session)
	sessions, _ := fetchSessions()
	for _, s := range sessions {
		sCopy := s
		knownSessions[s.ID] = &sCopy
	}

	// Track wallet balance for changes
	lastBalance := 0.0
	if wallet, err := fetchWallet(); err == nil {
		lastBalance = wallet.BalanceCKB
	}

	// Store recent events (max 10)
	var events []string
	addEvent := func(msg string) {
		timestamp := time.Now().Format("15:04:05")
		event := fmt.Sprintf("[%s] %s", timestamp, msg)
		events = append(events, event)
		if len(events) > 10 {
			events = events[1:]
		}
	}

	// Poll for changes every 2 seconds
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Initial draw
	drawDashboard(sessions, events)

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\n\n  Dashboard stopped.")
			return
		case <-ticker.C:
			// Check for new sessions or status changes
			sessions, err := fetchSessions()
			if err != nil {
				continue
			}

			hasChanges := false

			for _, s := range sessions {
				existing, found := knownSessions[s.ID]
				if !found {
					// New session!
					addEvent(fmt.Sprintf("NEW: %s CKB from %s",
						s.TotalPaid, truncateAddress(s.GuestAddress, 20)))
					sCopy := s
					knownSessions[s.ID] = &sCopy
					hasChanges = true
				} else if existing.Status != s.Status {
					// Status changed
					if s.Status == "expired" {
						addEvent(fmt.Sprintf("EXPIRED: %s",
							truncateAddress(s.GuestAddress, 20)))
					} else if s.Status == "settled" || s.Status == "ended" {
						addEvent(fmt.Sprintf("SETTLED: %s (%s CKB)",
							truncateAddress(s.GuestAddress, 20), s.TotalPaid))
					}
					sCopy := s
					knownSessions[s.ID] = &sCopy
					hasChanges = true
				} else if existing.TotalPaid != s.TotalPaid {
					// Payment amount changed (extension)
					addEvent(fmt.Sprintf("EXTENDED: %s now %s CKB",
						truncateAddress(s.GuestAddress, 20), s.TotalPaid))
					sCopy := s
					knownSessions[s.ID] = &sCopy
					hasChanges = true
				} else if existing.RemainingTime != s.RemainingTime {
					// Time changed (normal countdown)
					sCopy := s
					knownSessions[s.ID] = &sCopy
					hasChanges = true
				}
			}

			// Check wallet balance changes
			if wallet, err := fetchWallet(); err == nil {
				if wallet.BalanceCKB != lastBalance {
					diff := wallet.BalanceCKB - lastBalance
					if diff > 0 {
						addEvent(fmt.Sprintf("BALANCE: +%.2f CKB (Total: %.2f CKB)",
							diff, wallet.BalanceCKB))
					}
					lastBalance = wallet.BalanceCKB
					hasChanges = true
				}
			}

			// Always redraw to update remaining time
			drawDashboard(sessions, events)
			_ = hasChanges // used for potential optimization later
		}
	}
}

func drawDashboard(sessions []Session, events []string) {
	// Clear screen
	fmt.Print("\033[H\033[2J")

	connectURL := fmt.Sprintf("%s/", apiURL)

	fmt.Println("AirFi Host Dashboard")
	fmt.Println(strings.Repeat("─", 60))

	// Show wallet info
	wallet, err := fetchWallet()
	if err != nil {
		fmt.Printf("Wallet: Error - %s\n", err.Error())
	} else {
		status := "disconnected"
		if wallet.Connected {
			status = "connected"
		}
		fmt.Printf("Wallet:  %s (%s)\n", truncateAddress(wallet.Address, 40), status)
		fmt.Printf("Balance: %.2f CKB | Network: %s\n", wallet.BalanceCKB, wallet.Network)
	}

	fmt.Printf("Portal:  %s\n", connectURL)
	fmt.Printf("Updated: %s\n", time.Now().Format("15:04:05"))

	// Show sessions
	fmt.Println(strings.Repeat("─", 60))
	if len(sessions) == 0 {
		fmt.Println("Sessions: No active sessions")
	} else {
		activeCount := 0
		for _, s := range sessions {
			if s.Status == "active" {
				activeCount++
			}
		}
		fmt.Printf("Sessions: %d total (%d active)\n", len(sessions), activeCount)
		fmt.Println()
		fmt.Printf("  %-10s %-10s %-10s %-12s %s\n", "STATUS", "TYPE", "PAID", "TIME LEFT", "GUEST")
		fmt.Println("  " + strings.Repeat("-", 56))

		// Show max 8 sessions
		showCount := len(sessions)
		if showCount > 8 {
			showCount = 8
		}
		for i := 0; i < showCount; i++ {
			s := sessions[i]
			timeLeft := s.RemainingTime
			if timeLeft == "" {
				timeLeft = "-"
			}
			fmt.Printf("  %-10s %-10s %-10s %-12s %s\n",
				s.Status, s.Type, s.TotalPaid+" CKB", timeLeft, truncateAddress(s.GuestAddress, 18))
		}
		if len(sessions) > 8 {
			fmt.Printf("  ... and %d more\n", len(sessions)-8)
		}
	}

	// Show recent events
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println("Recent Events:")
	if len(events) == 0 {
		fmt.Println("  (waiting for activity...)")
	} else {
		for _, e := range events {
			fmt.Printf("  %s\n", e)
		}
	}

	fmt.Println(strings.Repeat("─", 60))
	fmt.Println("Press Ctrl+C to exit")
}

func displayInitialDashboard() {
	connectURL := fmt.Sprintf("%s/connect", apiURL)

	fmt.Println("\nAirFi WiFi Host Dashboard")
	fmt.Println("-------------------------")

	// Show wallet info
	wallet, err := fetchWallet()
	if err != nil {
		fmt.Printf("Wallet: Error - %s\n", err.Error())
	} else {
		status := "disconnected"
		if wallet.Connected {
			status = "connected"
		}
		fmt.Printf("Wallet:  %s (%s)\n", truncateAddress(wallet.Address, 50), status)
		fmt.Printf("Balance: %.2f CKB | Network: %s\n", wallet.BalanceCKB, wallet.Network)
	}

	fmt.Printf("URL:     %s\n", connectURL)

	// Show current sessions
	sessions, err := fetchSessions()
	if err != nil {
		fmt.Printf("Sessions: Error - %s\n", err.Error())
	} else if len(sessions) == 0 {
		fmt.Println("\nNo active sessions")
	} else {
		activeCount := 0
		for _, s := range sessions {
			if s.Status == "active" {
				activeCount++
			}
		}
		fmt.Printf("\nActive Sessions: %d\n", activeCount)
		for _, s := range sessions {
			sessionType := "channel"
			if s.Type == "prepaid" {
				sessionType = "prepaid"
			}
			fmt.Printf("  [%s] %s | %s | %s CKB | %s left\n",
				s.Status, sessionType, truncateAddress(s.GuestAddress, 25), s.TotalPaid, s.RemainingTime)
		}
	}
}

func displayQRCode() {
	fmt.Println("\nAirFi - Scan to Connect")
	fmt.Println("-----------------------")

	// Generate QR code with connection URL
	connectURL := fmt.Sprintf("%s/connect", apiURL)
	qrterminal.GenerateWithConfig(connectURL, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    os.Stdout,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 1,
	})

	fmt.Printf("\nURL: %s\n", connectURL)

	// Show wallet info
	showWalletCompact()

	// Keep running until interrupted
	fmt.Println("\nPress Ctrl+C to exit")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}

// Session represents a session from the API
type Session struct {
	ID            string `json:"session_id"`
	ChannelID     string `json:"channel_id"`
	GuestAddress  string `json:"guest_address"`
	Status        string `json:"status"`
	RemainingTime string `json:"remaining_time"`
	TotalPaid     string `json:"total_paid"`
	CreatedAt     string `json:"created_at"`
	Type          string `json:"type"` // "prepaid" or "channel"
}

// WalletInfo represents wallet info from the API
type WalletInfo struct {
	Address    string  `json:"address"`
	Balance    string  `json:"balance"`
	BalanceCKB float64 `json:"balance_ckb"`
	Connected  bool    `json:"connected"`
	Network    string  `json:"network"`
}

// HealthInfo represents health check response
type HealthInfo struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Connected bool   `json:"connected"`
}

// ChannelInfo represents channel info from the API
type ChannelInfo struct {
	ChannelID   string `json:"channel_id"`
	PeerAddress string `json:"peer_address"`
	State       string `json:"state"`
	MyBalance   string `json:"my_balance"`
	PeerBalance string `json:"peer_balance"`
}

func listSessions() {
	fmt.Println("\nActive Sessions")
	fmt.Println("---------------")

	// Fetch sessions from API
	sessions, err := fetchSessions()
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	if len(sessions) == 0 {
		fmt.Println("No active sessions")
		return
	}

	for _, s := range sessions {
		timeLeft := s.RemainingTime
		if timeLeft == "" {
			timeLeft = "-"
		}
		paid := s.TotalPaid
		if paid == "" {
			paid = "0"
		}
		fmt.Printf("[%s] %s | %s CKB | %s | %s\n",
			s.Status, s.Type, paid, timeLeft, truncateAddress(s.GuestAddress, 30))
	}
	fmt.Println()
}

func fetchSessions() ([]Session, error) {
	resp, err := httpClient.Get(fmt.Sprintf("%s/api/v1/sessions", apiURL))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Sessions []Session `json:"sessions"`
		Count    int       `json:"count"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result.Sessions, nil
}

func watchSessions() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		cancel()
	}()

	fmt.Println("Watching sessions... (Press Ctrl+C to exit)")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nStopped watching sessions")
			return
		case <-ticker.C:
			// Clear screen and refresh
			fmt.Print("\033[H\033[2J")
			fmt.Printf("AirFi Host Monitor - %s\n", time.Now().Format("15:04:05"))
			fmt.Println(strings.Repeat("─", 74))
			showWalletCompact()
			listSessions()
		}
	}
}

func settleChannel(channelID string) {
	fmt.Printf("\nSettling channel: %s\n", channelID)

	// Call API to settle channel
	url := fmt.Sprintf("%s/api/v1/channels/%s/settle", apiURL, channelID)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Printf("Failed to connect: %s\n", err.Error())
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		json.Unmarshal(body, &errResp)
		fmt.Printf("Settlement failed: %s\n", errResp["error"])
		return
	}

	var result ChannelInfo
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Failed to parse response: %s\n", err.Error())
		return
	}

	fmt.Println("Channel settled successfully!")
	fmt.Printf("Host:   %s CKB\n", result.MyBalance)
	fmt.Printf("Guest:  %s CKB\n", result.PeerBalance)
	fmt.Printf("Status: %s\n", result.State)
}

func showStatus() {
	fmt.Println("\nAirFi System Status")
	fmt.Println("-------------------")

	// Fetch health
	health, err := fetchHealth()
	if err != nil {
		fmt.Printf("API:     disconnected\n")
		fmt.Printf("Error:   %s\n", truncate(err.Error(), 50))
		return
	}

	status := "offline"
	if health.Status == "healthy" {
		status = "online"
	}

	connected := "no"
	if health.Connected {
		connected = "yes"
	}

	fmt.Printf("API:     %s\n", apiURL)
	fmt.Printf("Status:  %s\n", status)
	fmt.Printf("CKB:     %s\n", connected)

	// Show wallet
	showWallet()
}

func showWallet() {
	fmt.Println("\nHost Wallet")
	fmt.Println("-----------")

	wallet, err := fetchWallet()
	if err != nil {
		fmt.Printf("Error: %s\n", truncate(err.Error(), 50))
		return
	}

	fmt.Printf("Address: %s\n", wallet.Address)
	fmt.Printf("Balance: %.2f CKB\n", wallet.BalanceCKB)
	fmt.Printf("Network: %s\n", wallet.Network)
}

func showWalletCompact() {
	wallet, err := fetchWallet()
	if err != nil {
		fmt.Printf("Wallet: error - %s\n", err.Error())
		return
	}

	status := "disconnected"
	if wallet.Connected {
		status = "connected"
	}

	fmt.Printf("Wallet: %s | %.2f CKB (%s)\n",
		truncateAddress(wallet.Address, 20),
		wallet.BalanceCKB,
		status,
	)
}

func fetchHealth() (*HealthInfo, error) {
	resp, err := httpClient.Get(fmt.Sprintf("%s/health", apiURL))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var health HealthInfo
	if err := json.Unmarshal(body, &health); err != nil {
		return nil, err
	}

	return &health, nil
}

func fetchWallet() (*WalletInfo, error) {
	resp, err := httpClient.Get(fmt.Sprintf("%s/api/v1/wallet", apiURL))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var wallet WalletInfo
	if err := json.Unmarshal(body, &wallet); err != nil {
		return nil, err
	}

	return &wallet, nil
}

func formatStatus(status string) string {
	return status
}

func formatStatusCompact(status string) string {
	return status
}

func truncateAddress(addr string, maxLen int) string {
	if len(addr) <= maxLen {
		return addr
	}
	if maxLen < 10 {
		return addr[:maxLen]
	}
	half := (maxLen - 3) / 2
	return addr[:half] + "..." + addr[len(addr)-half:]
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// TokenResponse represents the JWT token response from the API
type TokenResponse struct {
	SessionID   string `json:"session_id"`
	AccessToken string `json:"access_token"`
	ExpiresAt   string `json:"expires_at"`
	ChannelID   string `json:"channel_id"`
	Error       string `json:"error"`
	Status      string `json:"status"`
	Message     string `json:"message"`
}

func getSessionToken(sessionID string) {
	fmt.Printf("\nGetting JWT token for session: %s\n", sessionID)
	fmt.Println(strings.Repeat("-", 50))

	resp, err := httpClient.Get(fmt.Sprintf("%s/api/v1/sessions/%s/token", apiURL, sessionID))
	if err != nil {
		fmt.Printf("Error: Failed to connect - %s\n", err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error: Failed to read response - %s\n", err.Error())
		return
	}

	var result TokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Error: Failed to parse response - %s\n", err.Error())
		return
	}

	if resp.StatusCode != http.StatusOK {
		if result.Status != "" {
			fmt.Printf("Status:  %s\n", result.Status)
		}
		if result.Message != "" {
			fmt.Printf("Message: %s\n", result.Message)
		} else if result.Error != "" {
			fmt.Printf("Error:   %s\n", result.Error)
		}
		return
	}

	fmt.Printf("Session: %s\n", result.SessionID)
	fmt.Printf("Channel: %s\n", result.ChannelID)
	fmt.Printf("Expires: %s\n", result.ExpiresAt)
	fmt.Println()
	fmt.Println("JWT Access Token:")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Println(result.AccessToken)
	fmt.Println(strings.Repeat("-", 50))
	fmt.Println("\nUse this token for WiFi authentication.")
}
