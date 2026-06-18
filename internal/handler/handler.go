package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"push-service/internal/models"
	"push-service/internal/scheduler"
	"push-service/internal/storage"
)

const (
	maxRequestBodySize = 100 << 20
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
	limitReader := io.LimitReader(r.Body, maxRequestBodySize)
	decoder := json.NewDecoder(limitReader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		if err == io.EOF || strings.Contains(err.Error(), "EOF") {
			return fmt.Errorf("request body is empty or truncated")
		}
		return err
	}
	if decoder.More() {
		return fmt.Errorf("request body exceeds %d MB limit", maxRequestBodySize/(1<<20))
	}
	return nil
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

	userIDs, inputCount, uniqueCount := validateAndDeduplicateUserIDs(req.UserIDs)
	if uniqueCount == 0 {
		errResponse(w, http.StatusBadRequest, "no valid user_ids after deduplication")
		return
	}

	if uniqueCount != inputCount {
		log.Printf("[Handler] CreateTask: input %d user_ids, %d unique (duplicates: %d)",
			inputCount, uniqueCount, inputCount-uniqueCount)
	}

	var grayUserIDs []string
	var grayCount int
	if req.GrayMode {
		if len(req.GrayUserIDs) == 0 {
			errResponse(w, http.StatusBadRequest, "gray_user_ids is required when gray_mode is true")
			return
		}
		grayUserIDs, _, grayCount = validateAndDeduplicateUserIDs(req.GrayUserIDs)
		if grayCount == 0 {
			errResponse(w, http.StatusBadRequest, "no valid gray_user_ids after deduplication")
			return
		}
		log.Printf("[Handler] CreateTask: gray mode enabled with %d gray users", grayCount)
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
		Name:        req.Name,
		UserIDs:     userIDs,
		Title:       req.Title,
		Content:     req.Content,
		Type:        req.Type,
		CronExpr:    req.CronExpr,
		PushAt:      req.PushAt,
		Repeat:      req.Repeat,
		GrayMode:    req.GrayMode,
		GrayUserIDs: grayUserIDs,
	}

	created := h.store.CreateTask(task)

	if err := h.scheduler.ScheduleTask(created); err != nil {
		errResponse(w, http.StatusBadRequest, "schedule task failed: "+err.Error())
		return
	}

	log.Printf("[Handler] Task %s created with %d unique users", created.ID, uniqueCount)

	resp := map[string]interface{}{
		"task":             created,
		"user_count":       uniqueCount,
		"input_user_count": inputCount,
		"gray_mode":        req.GrayMode,
	}
	if req.GrayMode {
		resp["gray_user_count"] = grayCount
	}

	okResponse(w, resp)
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

func (h *Handler) HandleGrayRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := extractPathID(r.URL.Path, "/api/tasks/")
	action := extractPathID(r.URL.Path, "/api/tasks/"+id+"/")
	if id == "" || action != "gray-release" {
		errResponse(w, http.StatusBadRequest, "invalid path")
		return
	}

	var req models.GrayReleaseRequest
	if err := parseJSON(r, &req); err != nil {
		errResponse(w, http.StatusBadRequest, "invalid request body: "+err.Error())
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

	switch req.Action {
	case "enable":
		if len(req.GrayUserIDs) == 0 {
			errResponse(w, http.StatusBadRequest, "gray_user_ids is required for enable action")
			return
		}
		grayUserIDs, _, grayCount := validateAndDeduplicateUserIDs(req.GrayUserIDs)
		if grayCount == 0 {
			errResponse(w, http.StatusBadRequest, "no valid gray_user_ids")
			return
		}
		task, err = h.store.SetGrayMode(id, true, grayUserIDs)
		if err != nil {
			errResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := h.scheduler.RescheduleTask(id); err != nil {
			errResponse(w, http.StatusBadRequest, "reschedule task failed: "+err.Error())
			return
		}
		log.Printf("[Handler] Task %s gray mode enabled with %d users", id, grayCount)
		okResponse(w, map[string]interface{}{
			"task":            task,
			"action":          "enabled",
			"gray_user_count": grayCount,
		})

	case "disable":
		task, err = h.store.SetGrayMode(id, false, nil)
		if err != nil {
			errResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := h.scheduler.RescheduleTask(id); err != nil {
			errResponse(w, http.StatusBadRequest, "reschedule task failed: "+err.Error())
			return
		}
		log.Printf("[Handler] Task %s gray mode disabled, full release to %d users", id, len(task.UserIDs))
		okResponse(w, map[string]interface{}{
			"task":       task,
			"action":     "disabled",
			"full_release": true,
			"user_count": len(task.UserIDs),
		})

	case "update_users":
		if len(req.GrayUserIDs) == 0 {
			errResponse(w, http.StatusBadRequest, "gray_user_ids is required for update_users action")
			return
		}
		if !task.GrayMode {
			errResponse(w, http.StatusBadRequest, "task is not in gray mode, enable first")
			return
		}
		grayUserIDs, _, grayCount := validateAndDeduplicateUserIDs(req.GrayUserIDs)
		if grayCount == 0 {
			errResponse(w, http.StatusBadRequest, "no valid gray_user_ids")
			return
		}
		task, err = h.store.SetGrayMode(id, true, grayUserIDs)
		if err != nil {
			errResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("[Handler] Task %s gray users updated to %d users", id, grayCount)
		okResponse(w, map[string]interface{}{
			"task":            task,
			"action":          "updated",
			"gray_user_count": grayCount,
		})

	default:
		errResponse(w, http.StatusBadRequest, "invalid action, must be one of: enable, disable, update_users")
	}
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

func validateAndDeduplicateUserIDs(userIDs []string) ([]string, int, int) {
	inputCount := len(userIDs)
	seen := make(map[string]bool, inputCount)
	result := make([]string, 0, inputCount)
	for _, uid := range userIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if !seen[uid] {
			seen[uid] = true
			result = append(result, uid)
		}
	}
	return result, inputCount, len(result)
}
