package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/agenthub/mcp-client-runtime/internal/mcp"
	"github.com/gin-gonic/gin"
)

// HTTPServer provides REST API for MCP client
type HTTPServer struct {
	router     *gin.Engine
	mcpManager *mcp.Manager
	port       int
}

// NewHTTPServer creates a new HTTP server
func NewHTTPServer(port int, mcpManager *mcp.Manager) *HTTPServer {
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	server := &HTTPServer{
		router:     router,
		mcpManager: mcpManager,
		port:       port,
	}

	server.setupRoutes()
	return server
}

// setupRoutes configures HTTP routes
func (s *HTTPServer) setupRoutes() {
	// Health check
	s.router.GET("/health", s.handleHealth)

	// Server management
	s.router.GET("/servers", s.handleListServers)
	s.router.POST("/servers", s.handleRegisterServer)
	s.router.POST("/servers/:name/start", s.handleStartServer)
	s.router.POST("/servers/:name/stop", s.handleStopServer)

	// Discovery
	s.router.GET("/servers/:name/tools", s.handleListTools)
	s.router.GET("/servers/:name/prompts", s.handleListPrompts)
	s.router.GET("/servers/:name/resources", s.handleListResources)

	// Execution (for testing/debugging)
	s.router.POST("/servers/:name/tools/:tool", s.handleExecuteTool)
}

// Start starts the HTTP server
func (s *HTTPServer) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("HTTP server listening on %s\n", addr)
	return s.router.Run(addr)
}

// ========== Handlers ==========

func (s *HTTPServer) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "mcp-client-runtime",
		"version": "1.0.0",
	})
}

func (s *HTTPServer) handleListServers(c *gin.Context) {
	servers := s.mcpManager.ListServers()
	c.JSON(http.StatusOK, gin.H{
		"servers": servers,
	})
}

type RegisterServerRequest struct {
	Name      string            `json:"name" binding:"required"`
	Command   string            `json:"command" binding:"required"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	AutoStart bool              `json:"autoStart"`
}

func (s *HTTPServer) handleRegisterServer(c *gin.Context) {
	var req RegisterServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Convert env map to array
	env := make([]string, 0, len(req.Env))
	for k, v := range req.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	config := mcp.ClientConfig{
		Name:    req.Name,
		Command: req.Command,
		Args:    req.Args,
		Env:     env,
	}

	if err := s.mcpManager.RegisterServer(config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Auto-start if requested
	if req.AutoStart {
		if err := s.mcpManager.StartServer(context.Background(), req.Name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   fmt.Sprintf("registered but failed to start: %v", err),
				"name":    req.Name,
				"started": false,
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "server registered successfully",
		"name":    req.Name,
		"started": req.AutoStart,
	})
}

func (s *HTTPServer) handleStartServer(c *gin.Context) {
	name := c.Param("name")

	if err := s.mcpManager.StartServer(context.Background(), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "server started successfully",
		"name":    name,
	})
}

func (s *HTTPServer) handleStopServer(c *gin.Context) {
	name := c.Param("name")

	if err := s.mcpManager.StopServer(name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "server stopped successfully",
		"name":    name,
	})
}

func (s *HTTPServer) handleListTools(c *gin.Context) {
	name := c.Param("name")

	client, err := s.mcpManager.GetClient(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	result, err := client.ListTools(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tools": result.Tools,
	})
}

func (s *HTTPServer) handleListPrompts(c *gin.Context) {
	name := c.Param("name")

	client, err := s.mcpManager.GetClient(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	result, err := client.ListPrompts(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"prompts": result.Prompts,
	})
}

func (s *HTTPServer) handleListResources(c *gin.Context) {
	name := c.Param("name")

	client, err := s.mcpManager.GetClient(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	result, err := client.ListResources(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"resources": result.Resources,
	})
}

type ExecuteToolRequest struct {
	Arguments map[string]interface{} `json:"arguments"`
}

func (s *HTTPServer) handleExecuteTool(c *gin.Context) {
	serverName := c.Param("name")
	toolName := c.Param("tool")

	var req ExecuteToolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, err := s.mcpManager.GetClient(serverName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	result, err := client.CallTool(context.Background(), toolName, req.Arguments)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"result":  result.Content,
		"isError": result.IsError,
	})
}
