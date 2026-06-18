package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"push-service/internal/models"
	"push-service/internal/storage"
)

type Scheduler struct {
	store      *storage.Store
	cronEngine *cron.Cron
	entries    map[string]cron.EntryID
	onceTimers map[string]*time.Timer
	mu         sync.Mutex
}

func NewScheduler(store *storage.Store) *Scheduler {
	c := cron.New(cron.WithParser(cron.NewParser(
		cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)))
	return &Scheduler{
		store:      store,
		cronEngine: c,
		entries:    make(map[string]cron.EntryID),
		onceTimers: make(map[string]*time.Timer),
	}
}

func (s *Scheduler) Start() {
	s.cronEngine.Start()
	log.Println("[Scheduler] Cron engine started")
}

func (s *Scheduler) Stop() {
	s.cronEngine.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.onceTimers {
		if t != nil {
			t.Stop()
		}
	}
	log.Println("[Scheduler] Cron engine stopped")
}

func (s *Scheduler) ScheduleTask(task *models.PushTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.removeTaskInternal(task.ID)

	if task.Status != "active" {
		log.Printf("[Scheduler] Task %s is not active, skipping scheduling", task.ID)
		return nil
	}

	if task.Repeat && task.CronExpr != "" {
		entryID, err := s.cronEngine.AddFunc(task.CronExpr, func() {
			s.ExecuteTask(task.ID)
		})
		if err != nil {
			return fmt.Errorf("invalid cron expression: %w", err)
		}
		s.entries[task.ID] = entryID
		log.Printf("[Scheduler] Scheduled recurring task %s with cron: %s", task.ID, task.CronExpr)
	} else if task.PushAt != nil {
		duration := time.Until(*task.PushAt)
		if duration <= 0 {
			if !task.Repeat {
				task.Status = "expired"
			}
			return fmt.Errorf("push time is in the past")
		}
		timer := time.AfterFunc(duration, func() {
			s.ExecuteTask(task.ID)
			s.mu.Lock()
			delete(s.onceTimers, task.ID)
			s.mu.Unlock()
		})
		s.onceTimers[task.ID] = timer
		log.Printf("[Scheduler] Scheduled one-time task %s at %v (in %v)", task.ID, task.PushAt, duration)
	}

	return nil
}

func (s *Scheduler) RemoveTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeTaskInternal(taskID)
}

func (s *Scheduler) removeTaskInternal(taskID string) {
	if entryID, ok := s.entries[taskID]; ok {
		s.cronEngine.Remove(entryID)
		delete(s.entries, taskID)
		log.Printf("[Scheduler] Removed cron entry for task %s", taskID)
	}
	if timer, ok := s.onceTimers[taskID]; ok {
		if timer != nil {
			timer.Stop()
		}
		delete(s.onceTimers, taskID)
		log.Printf("[Scheduler] Removed timer for task %s", taskID)
	}
}

func (s *Scheduler) ExecuteTask(taskID string) {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		log.Printf("[Scheduler] Error executing task %s: %v", taskID, err)
		return
	}

	if task.Status != "active" {
		log.Printf("[Scheduler] Task %s is %s, skipping execution", taskID, task.Status)
		return
	}

	totalUsers := len(task.UserIDs)
	log.Printf("[Scheduler] Executing task %s: %s, pushing to %d users", taskID, task.Name, totalUsers)

	notifType := task.Type
	if notifType == "" {
		notifType = "system"
	}

	if totalUsers == 0 {
		log.Printf("[Scheduler] Task %s has no users to push, skipping", taskID)
		return
	}

	batchSize := 500
	pushedCount := 0

	for i := 0; i < totalUsers; i += batchSize {
		end := i + batchSize
		if end > totalUsers {
			end = totalUsers
		}
		batch := task.UserIDs[i:end]
		notifs := s.store.BatchCreateNotifications(batch, task.Title, task.Content, notifType, taskID)
		pushedCount += len(notifs)
		log.Printf("[Scheduler] Task %s batch %d-%d: pushed %d notifications", taskID, i, end, len(notifs))
	}

	s.store.IncrementTaskPush(taskID)

	log.Printf("[Scheduler] Task %s completed: %d/%d users pushed", taskID, pushedCount, totalUsers)

	if !task.Repeat {
		_, err = s.store.UpdateTask(taskID, &models.UpdateTaskRequest{
			Status: stringPtr("completed"),
		})
		if err != nil {
			log.Printf("[Scheduler] Error updating task status: %v", err)
		}
		s.RemoveTask(taskID)
	}
}

func (s *Scheduler) RescheduleTask(taskID string) error {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		return err
	}
	return s.ScheduleTask(task)
}

func stringPtr(s string) *string {
	return &s
}
