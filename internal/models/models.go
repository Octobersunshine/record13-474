package models

import "time"

type Notification struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Type      string    `json:"type"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
}

type PushTask struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	UserIDs     []string  `json:"user_ids"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Type        string    `json:"type"`
	CronExpr    string    `json:"cron_expr,omitempty"`
	PushAt      *time.Time `json:"push_at,omitempty"`
	Repeat      bool      `json:"repeat"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastPushAt  *time.Time `json:"last_push_at,omitempty"`
	PushCount   int       `json:"push_count"`
	GrayMode    bool      `json:"gray_mode"`
	GrayUserIDs []string  `json:"gray_user_ids,omitempty"`
	FullReleaseAt *time.Time `json:"full_release_at,omitempty"`
}

type CreateTaskRequest struct {
	Name     string    `json:"name" binding:"required"`
	UserIDs  []string  `json:"user_ids" binding:"required"`
	Title    string    `json:"title" binding:"required"`
	Content  string    `json:"content" binding:"required"`
	Type     string    `json:"type"`
	CronExpr string    `json:"cron_expr"`
	PushAt   *time.Time `json:"push_at"`
	Repeat   bool      `json:"repeat"`
	GrayMode bool      `json:"gray_mode"`
	GrayUserIDs []string `json:"gray_user_ids"`
}

type UpdateTaskRequest struct {
	Name       *string    `json:"name"`
	UserIDs    *[]string  `json:"user_ids"`
	Title      *string    `json:"title"`
	Content    *string    `json:"content"`
	Type       *string    `json:"type"`
	CronExpr   *string    `json:"cron_expr"`
	PushAt     *time.Time `json:"push_at"`
	ClearPushAt bool      `json:"clear_push_at"`
	Repeat     *bool      `json:"repeat"`
	Status     *string    `json:"status"`
	GrayMode   *bool      `json:"gray_mode"`
	GrayUserIDs *[]string `json:"gray_user_ids"`
}

type GrayReleaseRequest struct {
	Action     string   `json:"action"`
	GrayUserIDs []string `json:"gray_user_ids,omitempty"`
}

type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}
