package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"push-service/internal/models"
)

var (
	ErrTaskNotFound       = errors.New("task not found")
	ErrNotificationNotFound = errors.New("notification not found")
)

type Store struct {
	mu            sync.RWMutex
	tasks         map[string]*models.PushTask
	notifications map[string][]*models.Notification
	taskNotifIdx  map[string][]string
}

func NewStore() *Store {
	return &Store{
		tasks:         make(map[string]*models.PushTask),
		notifications: make(map[string][]*models.Notification),
		taskNotifIdx:  make(map[string][]string),
	}
}

func GenerateID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return time.Now().Format("20060102150405") + hex.EncodeToString(make([]byte, 8))
	}
	return hex.EncodeToString(b)
}

func (s *Store) CreateTask(task *models.PushTask) *models.PushTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	task.ID = GenerateID()
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now
	task.Status = "active"
	task.PushCount = 0
	s.tasks[task.ID] = task
	return task
}

func (s *Store) GetTask(id string) (*models.PushTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return task, nil
}

func (s *Store) ListTasks() []*models.PushTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tasks := make([]*models.PushTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	return tasks
}

func (s *Store) UpdateTask(id string, req *models.UpdateTaskRequest) (*models.PushTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if req.Name != nil {
		task.Name = *req.Name
	}
	if req.UserIDs != nil {
		task.UserIDs = *req.UserIDs
	}
	if req.Title != nil {
		task.Title = *req.Title
	}
	if req.Content != nil {
		task.Content = *req.Content
	}
	if req.Type != nil {
		task.Type = *req.Type
	}
	if req.CronExpr != nil {
		task.CronExpr = *req.CronExpr
	}
	if req.ClearPushAt {
		task.PushAt = nil
	} else if req.PushAt != nil {
		task.PushAt = req.PushAt
	}
	if req.Repeat != nil {
		task.Repeat = *req.Repeat
	}
	if req.Status != nil {
		task.Status = *req.Status
	}
	task.UpdatedAt = time.Now()
	return task, nil
}

func (s *Store) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return ErrTaskNotFound
	}
	delete(s.tasks, id)
	delete(s.taskNotifIdx, id)
	return nil
}

func (s *Store) CreateNotification(userID, title, content, notifType, taskID string) *models.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	notif := &models.Notification{
		ID:        GenerateID(),
		UserID:    userID,
		Title:     title,
		Content:   content,
		Type:      notifType,
		IsRead:    false,
		CreatedAt: time.Now(),
	}
	s.notifications[userID] = append(s.notifications[userID], notif)
	if taskID != "" {
		s.taskNotifIdx[taskID] = append(s.taskNotifIdx[taskID], notif.ID)
	}
	return notif
}

func (s *Store) IncrementTaskPush(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[taskID]
	if !ok {
		return
	}
	now := time.Now()
	task.LastPushAt = &now
	task.PushCount++
	task.UpdatedAt = now
}

func (s *Store) ListUserNotifications(userID string, includeRead bool) []*models.Notification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	notifs, ok := s.notifications[userID]
	if !ok {
		return []*models.Notification{}
	}
	result := make([]*models.Notification, 0, len(notifs))
	for _, n := range notifs {
		if !includeRead && n.IsRead {
			continue
		}
		result = append(result, n)
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func (s *Store) MarkNotificationRead(userID, notificationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	notifs, ok := s.notifications[userID]
	if !ok {
		return ErrNotificationNotFound
	}
	now := time.Now()
	for _, n := range notifs {
		if n.ID == notificationID {
			n.IsRead = true
			n.ReadAt = &now
			return nil
		}
	}
	return ErrNotificationNotFound
}

func (s *Store) MarkAllNotificationsRead(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	notifs, ok := s.notifications[userID]
	if !ok {
		return 0
	}
	count := 0
	now := time.Now()
	for _, n := range notifs {
		if !n.IsRead {
			n.IsRead = true
			n.ReadAt = &now
			count++
		}
	}
	return count
}

func (s *Store) GetUnreadCount(userID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	notifs, ok := s.notifications[userID]
	if !ok {
		return 0
	}
	count := 0
	for _, n := range notifs {
		if !n.IsRead {
			count++
		}
	}
	return count
}
