package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/saalikmubeen/greenlight/internal/data"
	"github.com/saalikmubeen/greenlight/internal/validator"
)

// Endpoint for generating and sending activation tokens to your users.
// This can be useful if you need to re-send an activation token, such as when a user
// doesn’t activate their account within the 3-day time limit, or they never receive
// their welcome email.
func (app *application) createActivationTokenHandler(w http.ResponseWriter, r *http.Request) {
	// Parse and validate the user's email address.
	var input struct {
		Email string `json:"email"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}
	v := validator.New()

	if data.ValidateEmail(v, input.Email); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// Try to retrieve the corresponding user record for the email address. If it can't
	// be found, return an error message to the client.
	user, err := app.models.Users.GetByEmail(input.Email)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			v.AddError("email", "no matching email address found")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	// Return an error if the user has already been activated.
	if user.Activated {
		v.AddError("email", "user has already been activated")
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// Otherwise, create a new activation token.
	token, err := app.models.Tokens.New(user.ID, 3*24*time.Hour, data.ScopeActivation)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	// Email the user with their additional activation token in a background goroutine.
	app.background(func() {
		data := map[string]interface{}{
			"activationToken": token.Plaintext,
		}

		// Since email addresses MAY be case sensitive, notice that we are sending this
		// email using the address stored in our database for the user --- not to the
		// input.Email address provided by the client in this request.
		err = app.mailer.Send(user.Email, "token_activation.tmpl", data)
		if err != nil {
			app.logger.PrintError(err, nil)
		}
	})

	// Send a 202 Accepted response and confirmation message to the client.
	env := envelope{"message": "an email will be sent to you containing activation instructions"}
	err = app.writeJSON(w, http.StatusAccepted, env, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// Endpoint for logging in users and returning an authentication token.
// The user must provide their email address and password in the request body
// to be logged in and to receive an authentication token.
func (app *application) createAuthenticationTokenHandler(w http.ResponseWriter, r *http.Request) {
	// Parse the email and password from the request body.

	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	// Validate the email and password provided by the client.
	v := validator.New()
	data.ValidateEmail(v, input.Email)
	data.ValidatePasswordPlaintext(v, input.Password)

	if !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// Lookup the user record based on the email address. If no matching user was found, then we
	// call the app.invalidCredentialsResponse() helper to send a 401 Unauthorized response to
	// the client.
	user, err := app.models.Users.GetByEmail(input.Email)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.invalidCredentialsResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	// Check if the provided password matches the actual password for the user.
	match, err := user.Password.Matches(input.Password)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	// If the passwords don't match, then call the app.invalidCredentialsResponse() helper
	// and return
	if !match {
		app.invalidCredentialsResponse(w, r)
		return
	}

	// Otherwise, if the password is correct, we generate a new token with a 24-hour expiry time
	// and the scope 'authentication' (stateful authentication token).
	token, err := app.models.Tokens.New(user.ID, 24*time.Hour, data.ScopeAuthentication)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	// Encode the token to JSON and send it in the response along with a 201 Created status code.
	err = app.writeJSON(w, http.StatusCreated, envelope{"authentication_token": token}, nil)

	// after encoding the token to JSON, it will look like this:
	// {
	// 	"authentication_token": {
	// 		"token": "X3ASTT2CDAN66BACKSCI4SU7SI"
	// 		"expiry": "2021-07-01T15:00:00Z"
	// 	}
	// }

	// "token" above is the plaintext token and it's hash is stored in the database

	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// Handler for the password reset endpoint.
// Generate a password reset token and send it to the user's email address.
// A client sends a request to this endpoint with their email address in the request body
// to receive a password reset token or password reset link via email.
func (app *application) createPasswordResetTokenHandler(w http.ResponseWriter, r *http.Request) {
	// Parse and validate the user's email address.
	var input struct {
		Email string `json:"email"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	v := validator.New()
	if data.ValidateEmail(v, input.Email); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// Try to retrieve the corresponding user record for the email address. If it can't
	// be found, return an error message to the client.
	user, err := app.models.Users.GetByEmail(input.Email)

	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			v.AddError("email", "no matching email address found")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	// Return an error message if the user is not activated.
	if !user.Activated {
		v.AddError("email", "user account must be activated")
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// Otherwise, create a new password reset token with a 45-minute expiry time.
	token, err := app.models.Tokens.New(user.ID, 45*time.Minute, data.ScopePasswordReset)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	// Email the user with their password reset token.
	app.background(func() {
		data := map[string]interface{}{
			"passwordResetToken": token.Plaintext}
		// Since email addresses MAY be case sensitive, notice that we are sending this
		// email using the address stored in our database for the user --- not to the
		// input.Email address provided by the client in this request.
		err = app.mailer.Send(user.Email, "token_password_reset.tmpl", data)
		if err != nil {
			app.logger.PrintError(err, nil)
		}
	})

	// Send a 202 Accepted response and confirmation message to the client.
	env := envelope{"message": "an email will be sent to you containing password reset instructions"}
	err = app.writeJSON(w, http.StatusAccepted, env, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}

}

// Verify the password reset token and set a new password for the user.
// User sends a request to this endpoint with their new password and the password reset token.
func (app *application) updateUserPasswordHandler(w http.ResponseWriter, r *http.Request) {
	// Parse and validate the user's new password and password reset token.
	var input struct {
		Password       string `json:"password"`
		TokenPlaintext string `json:"token"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	v := validator.New()
	data.ValidatePasswordPlaintext(v, input.Password)
	data.ValidateTokenPlaintext(v, input.TokenPlaintext)
	if !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// Retrieve the details of the user associated with the password reset token,
	// returning an error message if no matching record was found.
	user, err := app.models.Users.GetForToken(data.ScopePasswordReset, input.TokenPlaintext)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			v.AddError("token", "invalid or expired password reset token")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	// Set the new password for the user.
	err = user.Password.Set(input.Password)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	// Save the updated user record in our database, checking for
	// any edit conflicts as normal.
	err = app.models.Users.Update(user)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	// If everything was successful, then delete all password reset tokens for the user.
	err = app.models.Tokens.DeleteAllForUser(data.ScopePasswordReset, user.ID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	// Send the user a confirmation message.
	env := envelope{"message": "your password was successfully reset"}
	err = app.writeJSON(w, http.StatusOK, env, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}

}

/*
* ** Token authentication (also sometimes known as bearer token authentication):

Authorization: Bearer <token>

Authentication tokens are sent back to the client in an Authorization header
like this: Authorization: Bearer <token>
rather than in the response body like we are doing in this project.

We can break down token authentication further into two sub-types:
1. stateful token authentication.
2. stateless token authentication.


Stateful token authentication:

In a stateful token approach, the value of the token is a high-entropy cryptographically
- secure random string. This token — or a fast hash of it — is stored server-side
in a database, alongside the user ID and an expiry time for the token.
When the client sends back the token in subsequent requests, your API can look up
the token in the database, check that it hasn't expired, and retrieve the corresponding
user ID to find out who the request is coming from.

The security is provided by the token being an 'unguessable',
which is why it's important to use a high-entropy cryptographically-secure
random value for the token.

Stateless token authentication:

In contrast, stateless tokens encode the user ID and expiry time in the token itself.
The token is cryptographically signed to prevent tampering and (in some cases) encrypted
to prevent the contents being read.
There are a few different technologies that you can use to create stateless
tokens. Encoding the information in a JWT (JSON Web Token) is probably the
most well-known approach, but PASETO, Branca and nacl/secretbox are viable alternatives too.

The main selling point of using stateless tokens for authentication is that
the work to encode and decode the token can be done in memory, and all the information
required to identify the user is contained within the token itself. There's no need
to perform a database lookup to find out who a request is coming from.


*/
