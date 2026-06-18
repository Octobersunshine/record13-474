package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"push-service/internal/models"
	"push-service/internal/scheduler"
	"push-service/internal/storage"
)

type Handler struct {
	store     *storage.Store
	scheduler *scheduler.Scheduler
}

func NewHandler(store *storage.Store, sched *scheduler.Scheduler) *Handler {
	return &Handler{
		store:     store,
		scheduler: sched,
	}
}

func writeJSON(w http.ResponseWriter, code int, resp *models.Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

func okResponse(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, &models.Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

func errResponse(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, &models.Response{
		Code:    code,
		Message: message,
	})
}

func parseJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req models.CreateTaskRequest
	if err := parseJSON(r, &req); err != nil {
		errResponse(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.UserIDs) == 0 {
		errResponse(w, http.StatusBadRequest, "user_ids is required")
		return
	}
	if req.Repeat && strings.TrimSpace(req.CronExpr) == "" {
		errResponse(w, http.StatusBadRequest, "cron_expr is required for repeat task")
		return
	}
	if !req.Repeat && req.PushAt == nil {
		errResponse(w, http.StatusBadRequest, "push_at is required for non-repeat task")
		return
	}

	task := &models.PushTask{
		Name:     req.Name,
		UserIDs:  req.UserIDs,
		Title:    req.Title,
		Content:  req.Content,
		Type:     req.Type,
		CronExpr: req.CronExpr,
		PushAt:   req.PushAt,
		Repeat:   req.Repeat,
	}

	created := h.store.CreateTask(task)

	if err := h.scheduler.ScheduleTask(created); err != nil {
		errResponse(w, http.StatusBadRequest, "schedule task failed: "+err.Error())
		return
	}

	okResponse(w, created)
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := extractPathID(r.URL.Path, "/api/tasks/")
	if id == "" {
		errResponse(w, http.StatusBadRequest, "task id is required")
		return
	}
	task, err := h.store.GetTask(id)
	if err != nil {
		if errors.Is(err, storage.ErrTaskNotFound) {
			errResponse(w, http.StatusNotFound, err.Error())
			return
		}
		errResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	okResponse(w, task)
}

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tasks := h.store.ListTasks()
	okResponse(w, tasks)
}

func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := extractPathID(r.URL.Path, "/api/tasks/")
	if id == "" {
		errResponse(w, http.StatusBadRequest, "task id is required")
		return
	}
	var req models.UpdateTaskRequest
	if err := parseJSON(r, &req); err != nil {
		errResponse(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	updated, err := h.store.UpdateTask(id, &req)
	if err != nil {
		if errors.Is(err, storage.ErrTaskNotFound) {
			errResponse(w, http.StatusNotFound, err.Error())
			return
		}
		errResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	if req.Status != nil || req.CronExpr != nil || req.ClearPushAt || req.PushAt != nil || req.Repeat != nil {
		if err := h.scheduler.RescheduleTask(id); err != nil {
			errResponse(w, http.StatusBadRequest, "reschedule task failed: "+err.Error())
			return
		}
	}

	okResponse(w, updated)
}

func (h *Handler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := extractPathID(r.URL.Path, "/api/tasks/")
	if id == "" {
		errResponse(w, http.StatusBadRequest, "task id is required")
		return
	}
	h.scheduler.RemoveTask(id)
	err := h.store.DeleteTask(id)
	if err != nil {
		if errors.Is(err, storage.ErrTaskNotFound) {
			errResponse(w, http.StatusNotFound, err.Error())
			return
		}
		errResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	okResponse(w, nil)
}

func (h *Handler) ExecuteTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := extractPathID(r.URL.Path, "/api/tasks/")
	action := extractPathID(r.URL.Path, "/api/tasks/"+id+"/")
	if id == "" || action != "execute" {
		errResponse(w, http.StatusBadRequest, "invalid path")
		return
	}
	task, err := h.store.GetTask(id)
	if err != nil {
		if errors.Is(err, storage.ErrTaskNotFound) {
			errResponse(w, http.StatusNotFound, err.Error())
			return
		}
		errResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.scheduler.ExecuteTask(id)
	okResponse(w, map[string]interface{}{
		"task_id":  id,
		"executed": true,
		"task":     task,
	})
}

func (h *Handler) ListUserNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := extractPathID(r.URL.Path, "/api/users/")
	suffix := strings.TrimPrefix(r.URL.Path, "/api/users/"+userID+"/")
	if userID == "" || suffix != "notifications" {
		errResponse(w, http.StatusBadRequest, "invalid path")
		return
	}
	includeRead := r.URL.Query().Get("include_read") == "true"
	notifs := h.store.ListUserNotifications(userID, includeRead)
	okResponse(w, notifs)
}

func (h *Handler) MarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := extractPathID(r.URL.Path, "/api/users/")
	rest := strings.TrimPrefix(r.URL.Path, "/api/users/"+userID+"/notifications/")
	if userID == "" || rest == "" {
		errResponse(w, http.StatusBadRequest, "invalid path")
		return
	}
	notifID := strings.TrimSuffix(rest, "/read")

	err := h.store.MarkNotificationRead(userID, notifID)
	if err != nil {
		if errors.Is(err, storage.ErrNotificationNotFound) {
			errResponse(w, http.StatusNotFound, err.Error())
			return
		}
		errResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	okResponse(w, map[string]string{"notification_id": notifID})
}

func (h *Handler) MarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := extractPathID(r.URL.Path, "/api/users/")
	suffix := strings.TrimPrefix(r.URL.Path, "/api/users/"+userID+"/")
	if userID == "" || suffix != "notifications/read-all" {
		errResponse(w, http.StatusBadRequest, "invalid path")
		return
	}
	count := h.store.MarkAllNotificationsRead(userID)
	okResponse(w, map[string]int{"marked_count": count})
}

func (h *Handler) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := extractPathID(r.URL.Path, "/api/users/")
	suffix := strings.TrimPrefix(r.URL.Path, "/api/users/"+userID+"/")
	if userID == "" || suffix != "notifications/unread-count" {
		errResponse(w, http.StatusBadRequest, "invalid path")
		return
	}
	count := h.store.GetUnreadCount(userID)
	okResponse(w, map[string]int{"unread_count": count})
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	okResponse(w, map[string]string{
		"status": "ok",
		"service": "push-service",
	})
}

func extractPathID(path, prefix string) string {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return ""
	}
	idx := strings.Index(rest, "/")
	if idx == -1 {
		return rest
	}
	return rest[:idx]
}
