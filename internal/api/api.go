// Package api wires HTTP handlers (Echo framework) for submission, status
// query, admin and ops.
package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"rc_lizhiqi/internal/ingest"
	"rc_lizhiqi/internal/metrics"
	"rc_lizhiqi/internal/observ"
)

// Server holds handler dependencies.
type Server struct {
	ingest   *ingest.Service
	observ   *observ.Service
	opsToken string
}

// New builds the API server.
func New(in *ingest.Service, ob *observ.Service, opsToken string) *Server {
	return &Server{ingest: in, observ: ob, opsToken: opsToken}
}

// Routes builds the configured Echo instance.
func (s *Server) Routes() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())

	e.POST("/notifications", s.handleSubmit)
	e.GET("/notifications/:id", s.handleStatus)
	e.GET("/admin/dead", s.handleListDead)
	e.POST("/admin/notifications/:id/replay", s.handleReplay)
	e.GET("/healthz", func(c echo.Context) error { return c.JSON(http.StatusOK, echo.Map{"status": "ok"}) })
	e.GET("/metrics", func(c echo.Context) error { return c.JSON(http.StatusOK, metrics.Snapshot()) })
	return e
}

func (s *Server) handleSubmit(c echo.Context) error {
	var req ingest.Request
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid JSON body"})
	}
	res, err := s.ingest.Accept(req)
	switch {
	case errors.Is(err, ingest.ErrUnknownType):
		return c.JSON(http.StatusUnprocessableEntity, echo.Map{"error": "unknown notification type"})
	case isValidation(err):
		return c.JSON(http.StatusUnprocessableEntity, echo.Map{"error": err.Error()})
	case err != nil:
		return c.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	default:
		code := http.StatusCreated
		if res.Duplicate {
			code = http.StatusOK // idempotent replay: same tracking id
		}
		return c.JSON(code, res)
	}
}

func (s *Server) handleStatus(c echo.Context) error {
	id := c.Param("id")
	detail := c.QueryParam("detail") == "true"
	if detail && !s.authorized(c) {
		return c.JSON(http.StatusForbidden, echo.Map{"error": "detail view requires ops authorization"})
	}
	v, err := s.observ.Status(id, detail)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "query failed"})
	}
	if v == nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "unknown tracking id"})
	}
	return c.JSON(http.StatusOK, v)
}

func (s *Server) handleListDead(c echo.Context) error {
	if !s.authorized(c) {
		return c.JSON(http.StatusForbidden, echo.Map{"error": "ops authorization required"})
	}
	list, err := s.observ.ListDead(100)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "query failed"})
	}
	out := make([]echo.Map, 0, len(list))
	for _, n := range list {
		out = append(out, echo.Map{
			"tracking_id": n.ID, "type": n.Type, "attempts": n.Attempts,
			"last_error": n.LastError, "last_response_code": n.LastResponseCode,
		})
	}
	return c.JSON(http.StatusOK, echo.Map{"dead": out})
}

func (s *Server) handleReplay(c echo.Context) error {
	if !s.authorized(c) {
		return c.JSON(http.StatusForbidden, echo.Map{"error": "ops authorization required"})
	}
	ok, err := s.observ.Replay(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "replay failed"})
	}
	if !ok {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "not found or not in DEAD state"})
	}
	return c.JSON(http.StatusOK, echo.Map{"status": "requeued"})
}

func (s *Server) authorized(c echo.Context) bool {
	tok := strings.TrimPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
	return s.opsToken != "" && tok == s.opsToken
}

func isValidation(err error) bool {
	var v ingest.ErrValidation
	return errors.As(err, &v)
}
