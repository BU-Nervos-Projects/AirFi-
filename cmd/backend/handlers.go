package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// handleIndex serves the landing page.
func (s *Server) handleIndex(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", gin.H{
		"title": "AirFi - WiFi Access",
	})
}

// handleConnect serves the connect/payment page.
func (s *Server) handleConnect(c *gin.Context) {
	// Capture MAC and IP from OpenNDS captive portal redirect
	mac := c.Query("mac")
	ip := c.Query("ip")

	c.HTML(http.StatusOK, "connect.html", gin.H{
		"title":      "Connect - AirFi",
		"macAddress": mac,
		"ipAddress":  ip,
	})
}

// handleSession serves the active session page.
func (s *Server) handleSession(c *gin.Context) {
	sessionID := c.Param("sessionId")

	// Check database for session
	dbSession, err := s.db.GetSession(sessionID)
	if err == nil {
		remaining := time.Until(dbSession.ExpiresAt)
		if remaining < 0 {
			remaining = 0
		}
		status := dbSession.Status
		if remaining <= 0 && status == "active" {
			status = "expired"
		}

		// Truncate session ID for display
		displayID := dbSession.ID
		if len(displayID) > 20 {
			displayID = displayID[:20] + "..."
		}

		channelDisplay := "Pending"
		if dbSession.ChannelID != "" {
			if len(dbSession.ChannelID) > 16 {
				channelDisplay = dbSession.ChannelID[:16] + "..."
			} else {
				channelDisplay = dbSession.ChannelID
			}
		}

		c.HTML(http.StatusOK, "session.html", gin.H{
			"title":         "Session - AirFi",
			"remainingTime": formatDuration(remaining),
			"session": gin.H{
				"ID":         displayID,
				"ChannelID":  channelDisplay,
				"BalanceCKB": fmt.Sprintf("%d", dbSession.BalanceCKB),
				"SpentCKB":   fmt.Sprintf("%d", dbSession.SpentCKB),
				"FundingCKB": fmt.Sprintf("%d", dbSession.FundingCKB),
				"Status":     status,
			},
		})
		return
	}

	// Check Perun channel session (in-memory)
	s.sessionsMu.RLock()
	session, exists := s.sessions[sessionID]
	s.sessionsMu.RUnlock()

	if !exists {
		c.Redirect(http.StatusFound, "/")
		return
	}

	remaining := time.Until(session.ExpiresAt)
	if remaining < 0 {
		remaining = 0
	}

	c.HTML(http.StatusOK, "session.html", gin.H{
		"title":         "Session - AirFi",
		"remainingTime": formatDuration(remaining),
		"session": gin.H{
			"ID":         session.ID,
			"ChannelID":  fmt.Sprintf("%x", session.Channel.ID())[:16] + "...",
			"BalanceCKB": fmt.Sprintf("%.0f", float64(session.FundingAmount.Int64()-session.TotalPaid.Int64())/100000000),
			"SpentCKB":   fmt.Sprintf("%.0f", float64(session.TotalPaid.Int64())/100000000),
			"FundingCKB": fmt.Sprintf("%.0f", float64(session.FundingAmount.Int64())/100000000),
			"Status":     "active",
		},
	})
}

// handleDashboard serves the host dashboard.
func (s *Server) handleDashboard(c *gin.Context) {
	authCookie, err := c.Cookie("airfi_host_auth")
	if err != nil || authCookie != s.dashboardPassword {
		c.Redirect(http.StatusFound, "/dashboard/login")
		return
	}

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"title": "Host Dashboard - AirFi",
	})
}

// handleDashboardLogin serves the dashboard login page.
func (s *Server) handleDashboardLogin(c *gin.Context) {
	c.HTML(http.StatusOK, "dashboard_login.html", gin.H{
		"title": "Login - Host Dashboard",
	})
}

// handleDashboardLoginPost handles dashboard login submission.
func (s *Server) handleDashboardLoginPost(c *gin.Context) {
	password := c.PostForm("password")

	if password == s.dashboardPassword {
		c.SetCookie("airfi_host_auth", password, 86400, "/", "", false, true)
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}

	c.HTML(http.StatusOK, "dashboard_login.html", gin.H{
		"title": "Login - Host Dashboard",
		"error": "Invalid password",
	})
}

// handleDashboardLogout handles dashboard logout.
func (s *Server) handleDashboardLogout(c *gin.Context) {
	c.SetCookie("airfi_host_auth", "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/dashboard/login")
}

// handleHealth returns server health status.
func (s *Server) handleHealth(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	_, err := s.hostClient.GetBalance(ctx)
	connected := err == nil

	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"connected": connected,
	})
}
