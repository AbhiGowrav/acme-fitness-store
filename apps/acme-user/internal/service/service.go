package service

import (
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	//stdopentracing "github.com/opentracing/opentracing-go"
	tracelog "github.com/opentracing/opentracing-go/log"
	"github.com/vmwarecloudadvocacy/user/internal/auth"
	"github.com/vmwarecloudadvocacy/user/internal/db"
	"github.com/vmwarecloudadvocacy/user/internal/tracer"
	"github.com/vmwarecloudadvocacy/user/pkg/logger"
)

// VerifyAuthToken checks to see if the JWT was present in blacklist table and validates it's authenticity
func VerifyAuthToken(c *gin.Context) {

	var accessTokenRequest auth.AccessTokenRequestBody

	err := c.ShouldBindJSON(&accessTokenRequest)
	if err != nil {
		message := err.Error()
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": message})
		return
	}

	foundInBlacklist := auth.IsBlacklisted(accessTokenRequest.AccessToken)

	if foundInBlacklist == true {
		logger.Logger.Infof("Found in Blacklist")
		c.JSON(http.StatusUnauthorized, gin.H{"status": http.StatusUnauthorized, "message": "Invalid Token"})
		c.Abort()
		return
	}

	valid, _, key, err := auth.ValidateToken(accessTokenRequest.AccessToken)
	if valid == false || err != nil {
		message := err.Error()
		logger.Logger.Errorf(message)
		c.JSON(http.StatusUnauthorized, gin.H{"status": http.StatusUnauthorized, "message": "Invalid Key. User Not Authorized"})
		c.Abort()
		return
	}

	// Make sure that key passed was not a refresh token
	if key != "signin_1" {
		logger.Logger.Errorf("Invalid Key Type")
		c.JSON(http.StatusUnauthorized, gin.H{"status": http.StatusUnauthorized, "message": "Provide a valid access token"})
		c.Abort()
		return
	}

	// Send StatusOK to indicate the access token was valid
	logger.Logger.Infof("Successfully verified user")
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "message": "Token Valid. User Authorized"})
}

// RefreshAccessToken method creates a new access_token, when the user provides an unexpired and validrefresh_token
func RefreshAccessToken(c *gin.Context) {

	var tokenRequest auth.RefreshTokenRequestBody

	err := c.ShouldBindJSON(&tokenRequest)
	if err != nil {
		message := err.Error()
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": message})
		return
	}

	valid, id, _, err := auth.ValidateToken(tokenRequest.RefreshToken)
	if valid == false || err != nil {
		message := err.Error()
		c.JSON(http.StatusUnauthorized, gin.H{"status": http.StatusUnauthorized, "message": message})
		c.Abort()
		return
	}

	if valid == true && id != "" {

		var user auth.UserResponse

		obId, _ := primitive.ObjectIDFromHex(id)
		// Retreive the username from users DB. This will verify if the user ID passed with JWT was legit or not.
		error := db.Collection.FindOne(*db.Context, bson.M{"_id": obId}).Decode(&user)

		if error != nil {
			message := "User " + error.Error()
			logger.Logger.Errorf(message)
			c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": "Invalid refresh token"})
			c.Abort()
			return
		}

		newToken, err := auth.GenerateAccessToken(user.Username, id)
		if err != nil {
			logger.Logger.Errorf(err.Error())
			c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": "Cannot Generate New Access Token"})
			c.Abort()
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "access_token": newToken, "refresh_token": tokenRequest.RefreshToken})
		c.Abort()
		return
	}

	c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": "Error Found "})

}

// GetUsers accepts a context and returns all the users in json format
func GetUsers(c *gin.Context) {
	var users []auth.UserResponse
	span, err := tracer.CreateTracerAndSpan("get_all_users", c)

	if err != nil {
		logger.Logger.Errorf(err.Error())
	}

	logger.Logger.Infof("Retrieving All Users")

	cursor, _ := db.Collection.Find(*db.Context, bson.M{})
	error := cursor.All(*db.Context, &users)

	if error != nil {
		tracer.OnErrorLog(span, error)
		message := "Users " + error.Error()
		c.JSON(http.StatusNotFound, gin.H{"status": http.StatusNotFound, "message": message})
		return
	}

	span.LogFields(
		tracelog.String("event", "success"),
		tracelog.Int("status", http.StatusOK),
	)
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "data": users})
}

// GetUser accepts context, User ID as param and returns user info
func GetUser(c *gin.Context) {
	var user auth.UserResponse

	span, err := tracer.CreateTracerAndSpan("get_user", c)

	if err != nil {
		logger.Logger.Errorf(err.Error())
	}

	userID := c.Param("id")

	if primitive.IsValidObjectID(userID) {

		id, _ := primitive.ObjectIDFromHex(userID)
		error := db.Collection.FindOne(*db.Context, bson.M{"_id": id}).Decode(&user)

		if error != nil {
			tracer.OnErrorLog(span, error)
			message := "User " + error.Error()
			c.JSON(http.StatusNotFound, gin.H{"status": http.StatusNotFound, "message": message})
			return
		}
	} else {
		span.LogFields(
			tracelog.String("event", "error"),
			tracelog.String("message", "Incorrect Format for UserID"),
		)
		message := "Incorrect Format for UserID"
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": message})
		return
	}

	span.LogFields(
		tracelog.String("event", "success"),
		tracelog.Int("status", http.StatusOK),
	)
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "data": user})
}

// RegisterUser accepts context and inserts data to the db
func RegisterUser(c *gin.Context) {

	var user auth.User

	error := c.ShouldBindJSON(&user)

	if error != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": "Incorrect Field Name(s)/ Value(s)"})
		return
	}

	error = user.Validate()

	if error != nil {
		message := "User " + error.Error()
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": message})
		return
	}

	// Inserts ID for the user object
	user.ID = primitive.NewObjectID()

	user.Password = auth.CalculatePassHash(user.Password, user.Salt)

	_, error = db.Collection.InsertOne(*db.Context, &user)

	if error != nil {
		message := "User " + error.Error()
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": message})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"status": http.StatusCreated, "message": "User created successfully!", "resourceId": user.ID})

}

// LoginUser Method
func LoginUser(c *gin.Context) {
	var user auth.User

	span, err := tracer.CreateTracerAndSpan("login", c)

	if err != nil {
		fmt.Println(err.Error())
	}

	err = c.ShouldBindJSON(&user)

	if err != nil {
		tracer.OnErrorLog(span, err)
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": "Incorrect Field Name(s)"})
		return
	}

	userpass := user.Password

	err = db.Collection.FindOne(*db.Context, bson.M{"username": user.Username}).Decode(&user)

	if err != nil {
		tracer.OnErrorLog(span, err)
		c.JSON(http.StatusUnauthorized, gin.H{"status": http.StatusUnauthorized, "message": "Invalid Username"})
		return
	}

	if user.Password != auth.CalculatePassHash(userpass, user.Salt) {
		c.JSON(http.StatusUnauthorized, gin.H{"status": http.StatusUnauthorized, "message": "Invalid Password"})
		return
	}

	accessToken, refreshToken, err := auth.GenerateTokenPair(user.Username, user.ID.Hex())
	if err != nil || accessToken == "" || refreshToken == "" {
		// Return if there is an error in creating the JWT return an internal server error
		tracer.OnErrorLog(span, err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "message": "Could not generate token"})
		return
	}

	span.LogFields(
		tracelog.String("event", "success"),
		tracelog.String("message", "returned token"),
		tracelog.Int("status", http.StatusOK),
	)
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "access_token": accessToken, "refresh_token": refreshToken})

}

// LogoutUser Method
func LogoutUser(c *gin.Context) {

	span, err := tracer.CreateTracerAndSpan("logout", c)

	if err != nil {
		logger.Logger.Errorf(err.Error())
		//fmt.Println(err.Error())
	}

	token := c.GetHeader("Authorization")

	if token == "" {
		span.LogFields(
			tracelog.String("event", "error"),
			tracelog.String("message", "Authorization token was not provided"),
		)
		logger.Logger.Errorf("Authorization token was not provided")
		c.JSON(http.StatusUnauthorized, gin.H{"status": http.StatusUnauthorized, "message": "Authorization Token is required"})
		c.Abort()
		return
	}

	extractedToken := strings.Split(token, "Bearer ")

	err = auth.InvalidateToken(extractedToken[1])
	if err != nil {
		tracer.OnErrorLog(span, err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "message": err.Error()})
		c.Abort()
		return
	}

	span.LogFields(
		tracelog.String("event", "success"),
		tracelog.Int("status", http.StatusAccepted),
	)
	c.JSON(http.StatusAccepted, gin.H{"status": http.StatusAccepted, "message": "Done"})

}

// DeleteUser accepts user id and deletes the specific user
func DeleteUser(c *gin.Context) {

	userID := c.Param("id")

	if primitive.IsValidObjectID(userID) != true {
		message := "Invalid User ID "
		c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": message})
		return
	}

	id, _ := primitive.ObjectIDFromHex(userID)
	_, error := db.Collection.DeleteOne(*db.Context, bson.M{"_id": id})

	if error != nil {
		message := "User " + error.Error()
		c.JSON(http.StatusNotFound, gin.H{"status": http.StatusNotFound, "message": message})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "message": "User deleted successfully!"})
}
