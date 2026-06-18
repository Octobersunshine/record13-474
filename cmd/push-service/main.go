package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"push-service/internal/handler"
	"push-service/internal/scheduler"
	"push-service/internal/storage"
)

func main() {
	store := storage.NewStore()
	sched := scheduler.NewScheduler(store)
	h := handler.NewHandler(store, sched)

	mux := http.NewServeMux()

	mux.HandleFunc("/health", h.HealthCheck)

	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.ListTasks(w, r)
		case http.MethodPost:
			h.CreateTask(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		path = strings.TrimSuffix(path, "/")
		parts := strings.Split(path, "/")

		if len(parts) == 1 && parts[0] != "" {
			switch r.Method {
			case http.MethodGet:
				h.GetTask(w, r)
			case http.MethodPut:
				h.UpdateTask(w, r)
			case http.MethodDelete:
				h.DeleteTask(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		} else if len(parts) == 2 && parts[1] == "execute" {
			if r.Method == http.MethodPost {
				h.ExecuteTask(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		} else if len(parts) == 2 && parts[1] == "gray-release" {
			if r.Method == http.MethodPost {
				h.HandleGrayRelease(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		} else {
			http.Error(w, "not found", http.StatusNotFound)
		}
	})

	mux.HandleFunc("/api/users/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/users/")
		path = strings.TrimSuffix(path, "/")
		parts := strings.Split(path, "/")

		switch {
		case len(parts) == 2 && parts[1] == "notifications":
			switch r.Method {
			case http.MethodGet:
				h.ListUserNotifications(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		case len(parts) == 3 && parts[1] == "notifications" && parts[2] == "unread-count":
			if r.Method == http.MethodGet {
				h.GetUnreadCount(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		case len(parts) == 3 && parts[1] == "notifications" && parts[2] == "read-all":
			if r.Method == http.MethodPost {
				h.MarkAllNotificationsRead(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		case len(parts) == 4 && parts[1] == "notifications" && parts[3] == "read":
			if r.Method == http.MethodPost {
				h.MarkNotificationRead(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})

	addr := ":8080"
	srv := &http.Server{
		Addr:         addr,
		Handler:      withCORS(withLogging(mux)),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	sched.Start()

	go func() {
		log.Printf("Push service starting on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down push service...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sched.Stop()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Push service exited successfully")
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("[HTTP] %s %s %d %v", r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
