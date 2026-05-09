package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type AuthConfig struct {
	JWTSecret    string
	JWTAlgorithm string
}

type AuthClaims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Type string `json:"type"`
	jwt.RegisteredClaims
}

const (
	contextUserID = "auth_user_id"
	contextRole   = "auth_role"
)

func RequireAuth(cfg AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawHeader := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(rawHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    http.StatusUnauthorized,
				"message": "Bearer access token is required.",
			})
			return
		}

		tokenString := strings.TrimSpace(strings.TrimPrefix(rawHeader, "Bearer "))
		claims := &AuthClaims{}

		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if cfg.JWTAlgorithm != "" && token.Method.Alg() != cfg.JWTAlgorithm {
				return nil, jwt.ErrTokenSignatureInvalid
			}
			return []byte(cfg.JWTSecret), nil
		})
		if err != nil || !token.Valid || claims.Type != "access" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    http.StatusUnauthorized,
				"message": "Access token is invalid or expired.",
			})
			return
		}

		userID, err := uuid.Parse(claims.Sub)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    http.StatusUnauthorized,
				"message": "Token subject is invalid.",
			})
			return
		}

		c.Set(contextUserID, userID)
		c.Set(contextRole, strings.ToUpper(strings.TrimSpace(claims.Role)))
		c.Next()
	}
}

func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role := strings.ToUpper(strings.TrimSpace(GetRole(c)))
		if role != "ADMIN" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    http.StatusForbidden,
				"message": "Admin role is required.",
			})
			return
		}
		c.Next()
	}
}

func RequireAnyRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[strings.ToUpper(strings.TrimSpace(role))] = struct{}{}
	}
	return func(c *gin.Context) {
		role := strings.ToUpper(strings.TrimSpace(GetRole(c)))
		if _, ok := allowed[role]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    http.StatusForbidden,
				"message": "You do not have permission for this action.",
			})
			return
		}
		c.Next()
	}
}

func GetUserID(c *gin.Context) (uuid.UUID, bool) {
	value, ok := c.Get(contextUserID)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := value.(uuid.UUID)
	return id, ok
}

func GetRole(c *gin.Context) string {
	value, ok := c.Get(contextRole)
	if !ok {
		return ""
	}
	role, _ := value.(string)
	return role
}
