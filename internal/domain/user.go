// internal/domain/user.go
package domain

import (
	"time"

	"gorm.io/gorm"
)

type Role string

const (
	RoleAdmin Role = "Admin"
	RoleUser  Role = "Siswa" // Default from user request
)

type User struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Name      string         `gorm:"type:varchar(100);not null" json:"name"`
	Email     string         `gorm:"type:varchar(100);uniqueIndex;not null" json:"email"`
	Password  string         `gorm:"type:varchar(255);not null" json:"-"`
	Role      Role           `gorm:"type:varchar(20);not null;default:'Siswa'" json:"role"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type UserRepository interface {
	Create(user *User) error
	FindByEmail(email string) (*User, error)
	FindByID(id uint) (*User, error)
	FindAll() ([]*User, error)
	Update(user *User) error
	Delete(id uint) error
}

type UserUseCase interface {
	CreateUser(user *User) error
	GetAllUsers() ([]*User, error)
	GetUserByID(id uint) (*User, error)
	UpdateUser(id uint, name *string, role *Role, password *string) error
	DeleteUser(id uint) error
}

type AuthUseCase interface {
	Login(email, password string) (string, string, error) // AccessToken, RefreshToken, Error
	RefreshToken(token string) (string, error)
	UpdateProfile(id uint, name *string, password *string) error
}
