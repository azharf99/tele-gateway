// internal/usecase/auth_usecase.go
package usecase

import (
	"errors"
	"os"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type authUseCase struct {
	userRepo domain.UserRepository
}

func NewAuthUseCase(userRepo domain.UserRepository) domain.AuthUseCase {
	return &authUseCase{userRepo: userRepo}
}

func (u *authUseCase) Login(email, password string) (string, string, error) {
	user, err := u.userRepo.FindByEmail(email)
	if err != nil {
		return "", "", errors.New("invalid credentials")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", "", errors.New("invalid credentials")
	}

	accessToken, err := u.generateToken(user, 15*time.Minute, os.Getenv("JWT_SECRET"))
	if err != nil {
		return "", "", err
	}

	refreshToken, err := u.generateToken(user, 7*24*time.Hour, os.Getenv("JWT_REFRESH_SECRET"))
	if err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

func (u *authUseCase) RefreshToken(tokenString string) (string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(os.Getenv("JWT_REFRESH_SECRET")), nil
	})

	if err != nil || !token.Valid {
		return "", errors.New("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid claims")
	}

	userID := uint(claims["user_id"].(float64))
	user, err := u.userRepo.FindByID(userID)
	if err != nil {
		return "", err
	}

	return u.generateToken(user, 15*time.Minute, os.Getenv("JWT_SECRET"))
}

func (u *authUseCase) generateToken(user *domain.User, duration time.Duration, secret string) (string, error) {
	claims := jwt.MapClaims{
		"user_id": user.ID,
		"role":    user.Role,
		"name":    user.Name,
		"email":   user.Email,
		"exp":     time.Now().Add(duration).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func (u *authUseCase) UpdateProfile(id uint, email *string, password *string) error {
	user, err := u.userRepo.FindByID(id)
	if err != nil {
		return errors.New("user not found")
	}

	if email != nil && *email != "" {
		// optionally validate email format here
		user.Email = *email
	}

	if password != nil && *password != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return errors.New("failed to hash password")
		}
		user.Password = string(hashedPassword)
	}

	return u.userRepo.Update(user)
}
