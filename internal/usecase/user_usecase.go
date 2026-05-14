// internal/usecase/user_usecase.go
package usecase

import (
	"errors"

	"github.com/azharf99/tele-gateway/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

type userUseCase struct {
	userRepo domain.UserRepository
}

func NewUserUseCase(userRepo domain.UserRepository) domain.UserUseCase {
	return &userUseCase{userRepo: userRepo}
}

func (u *userUseCase) CreateUser(user *domain.User) error {
	if user.Email == "" {
		return errors.New("email is required")
	}
	if _, err := u.userRepo.FindByEmail(user.Email); err == nil {
		return errors.New("email already exists")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user.Password = string(hashedPassword)

	return u.userRepo.Create(user)
}

func (u *userUseCase) GetAllUsers() ([]*domain.User, error) {
	return u.userRepo.FindAll()
}

func (u *userUseCase) GetUserByID(id uint) (*domain.User, error) {
	return u.userRepo.FindByID(id)
}

func (u *userUseCase) UpdateUser(id uint, name *string, role *domain.Role, password *string) error {
	user, err := u.userRepo.FindByID(id)
	if err != nil {
		return errors.New("user not found")
	}

	if name != nil && *name != "" {
		user.Name = *name
	}

	if role != nil {
		user.Role = *role
	}

	if password != nil && *password != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		user.Password = string(hashedPassword)
	}

	return u.userRepo.Update(user)
}

func (u *userUseCase) DeleteUser(id uint) error {
	return u.userRepo.Delete(id)
}
