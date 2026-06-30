# ADR-0010: HTTP-Router mit gorilla/mux

## Status

Akzeptiert

## Kontext

Die Anwendung benötigt einen HTTP-Router für die REST-API mit folgenden Anforderungen:

- URL-Parameter (z.B. `/api/v1/packages/{packageId}`)
- Middleware-Unterstützung (Logging, Rate-Limiting, CORS)
- Query-Parameter-Handling
- Subrouter für API-Versionierung
- Gute Performance

### Alternativen

| Router | Vorteile | Nachteile |
|--------|----------|-----------|
| gorilla/mux | Standard, ausgereift, flexibel | Projekt archiviert (aber stabil) |
| chi | Modern, middleware-freundlich | Weniger verbreitet |
| gin | Schnell, viele Features | Framework statt Library |
| net/http | Keine Abhängigkeiten | Limitierte Routing-Features |
| httprouter | Sehr schnell | Keine Middleware-Unterstützung |

## Entscheidung

Wir verwenden **gorilla/mux** als HTTP-Router.

### Begründung

- **Industriestandard:** gorilla/mux ist einer der am weitesten verbreiteten Go-Router. Das Archivieren des Projekts bedeutet nur, dass es als "fertig" gilt – der Code ist stabil und produktionsreif.
- **Erweiterbarkeit:** Das Middleware-Pattern ermöglicht es, neue Crosscutting-Concerns (Authentifizierung, Tracing, etc.) ohne Änderungen am Router hinzuzufügen.
- **Kompatibilität:** gorilla/mux ist vollständig kompatibel mit `net/http`, sodass Standard-Go-Handler verwendet werden können.
- **Feature-Komplett:** URL-Variablen, Query-Parameter, Subrouter, Method-Matching – alle benötigten Features sind vorhanden.

### Router-Struktur

```go
// internal/adapters/primary/http/router.go
package http

import (
    "github.com/gorilla/mux"
)

func NewRouter(
    queryHandler *handlers.QueryHandler,
    packageHandler *handlers.PackageHandler,
    healthHandler *handlers.HealthHandler,
    openapiHandler *handlers.OpenAPIHandler,
) *mux.Router {
    r := mux.NewRouter()

    // API v1 Subrouter
    api := r.PathPrefix("/api/v1").Subrouter()

    // Query-Endpunkte
    api.HandleFunc("/query", queryHandler.Query).Methods("GET")
    api.HandleFunc("/query/{packageId}", queryHandler.QueryByPackage).Methods("GET")

    // Package-Endpunkte
    api.HandleFunc("/packages", packageHandler.List).Methods("GET")
    api.HandleFunc("/packages/{packageId}", packageHandler.Get).Methods("GET")
    api.HandleFunc("/packages/{packageId}/layers", packageHandler.GetLayers).Methods("GET")
    api.HandleFunc("/packages/{packageId}/metadata", packageHandler.GetMetadata).Methods("GET")

    // OpenAPI
    api.HandleFunc("/openapi.yaml", openapiHandler.Spec).Methods("GET")

    // Health-Endpunkte
    r.HandleFunc("/health/ready", healthHandler.Ready).Methods("GET")
    r.HandleFunc("/health/live", healthHandler.Live).Methods("GET")

    return r
}
```

### URL-Parameter extrahieren

```go
// internal/adapters/primary/http/handlers/query.go
package handlers

import (
    "net/http"
    "strconv"

    "github.com/gorilla/mux"
)

func (h *QueryHandler) QueryByPackage(w http.ResponseWriter, r *http.Request) {
    // URL-Parameter
    vars := mux.Vars(r)
    packageID := vars["packageId"]

    // Query-Parameter
    lon, _ := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
    lat, _ := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
    srid, _ := strconv.Atoi(r.URL.Query().Get("srid"))

    // ...
}
```

### Middleware-Integration

```go
// internal/adapters/primary/http/server.go
package http

import (
    "github.com/gorilla/mux"
    "github.com/gorilla/handlers"
)

func NewServer(cfg Config, router *mux.Router) *Server {
    // Middleware-Stack aufbauen
    handler := router

    // Recovery (Panic-Handler)
    handler = middleware.Recovery(handler)

    // Logging
    handler = middleware.Logging(cfg.Logging)(handler)

    // Rate-Limiting
    if cfg.RateLimit.Enabled {
        handler = middleware.RateLimit(cfg.RateLimit)(handler)
    }

    // CORS (falls aktiviert)
    if cfg.CORS.Enabled {
        handler = handlers.CORS(
            handlers.AllowedOrigins(cfg.CORS.AllowedOrigins),
            handlers.AllowedMethods([]string{"GET", "OPTIONS"}),
            handlers.AllowedHeaders([]string{"Content-Type", "Accept"}),
        )(handler)
    }

    // Request-ID
    handler = middleware.RequestID(handler)

    return &Server{
        httpServer: &http.Server{
            Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
            Handler:      handler,
            ReadTimeout:  cfg.Server.ReadTimeout,
            WriteTimeout: cfg.Server.WriteTimeout,
        },
    }
}
```

### Custom Middleware

```go
// internal/adapters/primary/http/middleware/logging.go
package middleware

import (
    "log/slog"
    "net/http"
    "time"
)

func Logging(cfg LoggingConfig) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()

            // Response-Writer wrappen für Status-Code-Capture
            wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

            next.ServeHTTP(wrapped, r)

            if cfg.LogRequests {
                slog.Info("request",
                    "method", r.Method,
                    "path", r.URL.Path,
                    "status", wrapped.statusCode,
                    "duration", time.Since(start),
                    "request_id", r.Header.Get("X-Request-ID"),
                )
            }
        })
    }
}

type responseWriter struct {
    http.ResponseWriter
    statusCode int
}

func (w *responseWriter) WriteHeader(code int) {
    w.statusCode = code
    w.ResponseWriter.WriteHeader(code)
}
```

### Subrouter für Versionierung

```go
// Zukünftige API v2
apiV2 := r.PathPrefix("/api/v2").Subrouter()
apiV2.HandleFunc("/query", queryHandlerV2.Query).Methods("GET")
```

## Konsequenzen

### Positiv

- **Erweiterbar:** Neue Endpunkte und Middleware ohne Architekturänderungen
- **Standardkonform:** Bewährte Patterns der Go-Community
- **net/http-kompatibel:** Standard-Handler funktionieren ohne Anpassung
- **Flexibel:** URL-Variablen, Query-Parameter, Method-Matching

### Negativ

- **Archiviert:** gorilla/mux wird nicht mehr aktiv entwickelt (aber stabil)
- **Performance:** Nicht der schnellste Router (aber ausreichend für diesen Use-Case)

### Mitigationen

- Bei Performance-Problemen kann später auf chi gewechselt werden (ähnliche API)
- Die Handler-Logik ist vom Router entkoppelt

## Referenzen

- [gorilla/mux](https://github.com/gorilla/mux)
- [gorilla/handlers](https://github.com/gorilla/handlers)
