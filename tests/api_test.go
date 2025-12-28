package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupTestRouter creates a Gin router for testing.
func setupTestRouter() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	// Add test routes
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"connected": true,
		})
	})

	router.GET("/api/v1/pricing", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"ckbytes_per_minute": "833333333",
			"prices": gin.H{
				"5_minutes":  "4166666665",
				"30_minutes": "25000000000",
				"1_hour":     "50000000000",
			},
		})
	})

	router.GET("/api/v1/wallet", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"address":     "ckt1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsq",
			"balance":     "50000000000",
			"balance_ckb": 500.0,
			"network":     "testnet",
			"connected":   true,
		})
	})

	router.GET("/api/v1/sessions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"sessions": []gin.H{},
			"count":    0,
		})
	})

	router.GET("/api/v1/sessions/:sessionId", func(c *gin.Context) {
		sessionID := c.Param("sessionId")
		if sessionID == "test-session-1" {
			c.JSON(http.StatusOK, gin.H{
				"session_id":     "test-session-1",
				"channel_id":     "channel-1",
				"guest_address":  "ckt1guest",
				"status":         "active",
				"remaining_time": "59m 30s",
				"total_paid":     "50000000000",
			})
		} else {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		}
	})

	router.POST("/api/v1/sessions/:sessionId/extend", func(c *gin.Context) {
		sessionID := c.Param("sessionId")
		var req struct {
			Amount string `json:"amount"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"session_id":     sessionID,
			"token":          "new-token",
			"remaining_time": "1h 29m",
			"total_paid":     "75000000000",
		})
	})

	router.POST("/api/v1/sessions/:sessionId/end", func(c *gin.Context) {
		sessionID := c.Param("sessionId")
		c.JSON(http.StatusOK, gin.H{
			"session_id": sessionID,
			"status":     "ended",
		})
	})

	return router
}

func TestHealthCheck(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", response["status"])
	}
}

func TestGetPricing(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest("GET", "/api/v1/pricing", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["ckbytes_per_minute"] == nil {
		t.Error("Expected ckbytes_per_minute in response")
	}
	if response["prices"] == nil {
		t.Error("Expected prices in response")
	}
}

func TestGetWalletStatus(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest("GET", "/api/v1/wallet", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["address"] == nil {
		t.Error("Expected address in response")
	}
	if response["balance"] == nil {
		t.Error("Expected balance in response")
	}
	if response["connected"] != true {
		t.Error("Expected connected to be true")
	}
}

func TestListSessions_Empty(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest("GET", "/api/v1/sessions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["count"].(float64) != 0 {
		t.Errorf("Expected count 0, got %v", response["count"])
	}
}

func TestGetSession_Found(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest("GET", "/api/v1/sessions/test-session-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["session_id"] != "test-session-1" {
		t.Errorf("Expected session_id 'test-session-1', got %v", response["session_id"])
	}
	if response["status"] != "active" {
		t.Errorf("Expected status 'active', got %v", response["status"])
	}
}

func TestGetSession_NotFound(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest("GET", "/api/v1/sessions/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestExtendSession(t *testing.T) {
	router := setupTestRouter()

	body := map[string]string{"amount": "25000000000"}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "/api/v1/sessions/test-session-1/extend", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["session_id"] != "test-session-1" {
		t.Errorf("Expected session_id 'test-session-1', got %v", response["session_id"])
	}
	if response["token"] == nil {
		t.Error("Expected token in response")
	}
}

func TestExtendSession_InvalidBody(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest("POST", "/api/v1/sessions/test-session-1/extend", bytes.NewBufferString("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestEndSession(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest("POST", "/api/v1/sessions/test-session-1/end", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["status"] != "ended" {
		t.Errorf("Expected status 'ended', got %v", response["status"])
	}
}

func TestCORSHeaders(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req, _ := http.NewRequest("OPTIONS", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204 for OPTIONS, got %d", w.Code)
	}

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("Expected CORS header")
	}
}

func TestJSONContentType(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json; charset=utf-8" {
		t.Errorf("Expected JSON content type, got %s", contentType)
	}
}
