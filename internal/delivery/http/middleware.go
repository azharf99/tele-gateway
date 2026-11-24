// internal/delivery/http/middleware.go
package http

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !validateJWT(c) {
			return
		}
		c.Next()
	}
}

// APIKeyOrAdminMiddleware authenticates B2B requests via X-API-Key (constant-time
// matched against validKeys) or falls back to a JWT Bearer token with RoleAdmin.
// When an X-API-Key header is present but invalid, it fails immediately with 401
// without falling back to JWT.
func APIKeyOrAdminMiddleware(validKeys []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := strings.TrimSpace(c.GetHeader("X-API-Key"))
		if apiKey != "" {
			for _, candidate := range validKeys {
				if candidate != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(candidate)) == 1 {
					c.Set("auth_type", "api_key")
					c.Set("role", domain.RoleAdmin)
					c.Next()
					return
				}
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			return
		}

		if !validateJWT(c) {
			return
		}

		role, exists := c.Get("role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Role not found"})
			return
		}
		if userRole, ok := role.(domain.Role); !ok || userRole != domain.RoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Access denied"})
			return
		}

		c.Set("auth_type", "jwt")
		c.Next()
	}
}

func validateJWT(c *gin.Context) bool {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
		return false
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header format must be Bearer {token}"})
		return false
	}

	tokenString := parts[1]
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(os.Getenv("JWT_SECRET")), nil
	})

	if err != nil || !token.Valid {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
		return false
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid claims"})
		return false
	}

	userIDFloat, ok := claims["user_id"].(float64)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid claims"})
		return false
	}
	roleStr, _ := claims["role"].(string)
	nameStr, _ := claims["name"].(string)
	emailStr, _ := claims["email"].(string)

	c.Set("user_id", uint(userIDFloat))
	c.Set("role", domain.Role(roleStr))
	c.Set("name", nameStr)
	c.Set("email", emailStr)
	return true
}

func RoleMiddleware(roles ...domain.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{"error": "Role not found"})
			c.Abort()
			return
		}

		userRole := role.(domain.Role)
		for _, r := range roles {
			if userRole == r {
				c.Next()
				return
			}
		}

		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		c.Abort()
	}
}
