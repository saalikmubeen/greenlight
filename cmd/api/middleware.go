package main

import (
	"errors"
	"expvar"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/tomasen/realip"
	"golang.org/x/time/rate"

	"github.com/saalikmubeen/greenlight/internal/data"
	"github.com/saalikmubeen/greenlight/internal/validator"
)

/* Any panics in our API handlers will be recovered automatically by Go’s http.Server.
This behavior is OK, but it would be better for the client if we could also send a
500 Internal Server Error response to explain that something has gone wrong —
rather than just closing the HTTP connection with no context.
*/

// recoverPanic is middleware that recovers from a panic by responding with a 500 Internal Server
// Error before closing the connection. It will also log the error using our custom Logger at
// the ERROR level.
// Our middleware will only recover panics that happen in the same goroutine that
// executed the recoverPanic() middleware.
// If, for example, you have a handler which spins up another goroutine
// (e.g. to do some background processing), then any panics that happen in the
// background goroutine will not be recovered — not by the recoverPanic() middleware...
// and not by the panic recovery built into http.Server. These panics will cause your
// application to exit and bring down the server.
func (app *application) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create a deferred function (which will always be run in the event of a panic as
		// Go unwinds the stack).
		defer func() {
			// Use the builtin recover function to check if there has been a panic or not.
			if err := recover(); err != nil {
				// If there was a panic, set a "Connection: close" header on the response. This
				// acts a trigger to make Go's HTTP server automatically close the current
				// connection after a response has been sent.
				w.Header().Set("Connection:", "close")
				// The value returned by recover() has the type interface{}, so we use
				// fmt.Errorf() to normalize it into an error and call our
				// serverErrorResponse() helper. In turn, this will log the error using our
				// custom Logger type at the ERROR level and send the client a
				// 500 Internal Server Error response.
				app.serverErrorResponse(w, r, fmt.Errorf("%s", err))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ** Token Bucket rate limiter:
/*
x/time/rate package provides a tried-and-tested implementation of a "token bucket rate limiter".
A Limiter controls how frequently events are allowed to happen. It implements a
“token bucket” of size b , initially full and refilled at rate r tokens per second.
Putting that into the context of our API application...
   - We will have a bucket that starts with b tokens in it.
   - Each time we receive a HTTP request, we will remove one token from the bucket.
   - Every 1/r seconds, a token is added back to the bucket — up to a maximum of b total tokens
     or we can say that the bucket is refilled at a rate of r tokens per second.
   - If we receive a HTTP request and the bucket is empty, then we should return a
   - 429 Too Many Requests response.

In practice this means that our application would allow a maximum ‘burst’ of b HTTP
requests in quick succession, but over time it would allow an average of r requests per second.


Note that the Limit type is an 'alias' for float64:
func NewLimiter(r Limit, b int) *Limiter

Allow 2 requests per second, with a maximum of 4 requests in a burst.
In other words token bucket with a capacity of 4 tokens, and a refill rate of 2 tokens per second.
limiter := rate.NewLimiter(2, 4)
*/

// Global rate limiter:
// This will consider all the requests that our API receives
// (rather than having separate rate limiters for every individual client).
func (app *application) globalRateLimit(next http.Handler) http.Handler {
	// Initialize a new rate limiter which allows an average of 2 requests per second,
	// with a maximum of 4 requests in a single ‘burst’.
	limiter := rate.NewLimiter(2, 4)
	// The function we are returning is a closure, which 'closes over'
	// the limiter variable.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Call limiter.Allow() to see if the request is permitted, and if it's not,
		// then we call the rateLimitExceededResponse() helper to return a 429 Too Many
		// Requests response (we will create this helper in a minute).

		// Whenever we call the Allow() method on the rate limiter exactly one token
		//  will be consumed from the bucket. If there are no tokens left in the bucket,
		// then Allow() will return false and that acts as the trigger for us send the
		// client a 429 Too Many Requests response.
		if !limiter.Allow() {
			app.rateLimitExceededResponse(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// IP-based Rate Limiting:
// A separate rate limiter for each client, so that one bad client making too
// many requests doesn’t affect all the others.
// Create an in-memory map of rate limiters, using the IP address for each client as the map key.
func (app *application) rateLimit(next http.Handler) http.Handler {
	// Define a client struct to hold the rate limiter and last seen time for reach client
	// ! one time initialization
	// This is a one time initialization of the client struct, meaning that it will only
	// be run once when the application starts up. And after that the same client struct
	// will be available to each request.
	type client struct {
		limiter  *rate.Limiter
		lastSeen time.Time
	}

	// Declare a mutex and a map to hold pointers to a client struct.
	var (
		mu      sync.Mutex
		clients = make(map[string]*client)
	)

	// Launch a background goroutine which removes old entries (any clients that we haven’t
	// been seen recently from the clients map) from the clients map once every minute.
	go func() {
		for range time.Tick(time.Minute) {
			// Or instead of using for range with time.Tick we can
			// use simple for loop with time.Sleep as:
			// for {
			// 	time.Sleep(time.Minute)
			//
			//   rest of code ...
			// }

			// Lock the mutex to prevent any rate limiter checks from happening while the cleanup
			// is taking place.
			mu.Lock()

			// Loop through all clients. if they haven't been seen within the last three minutes,
			// then delete the corresponding entry from the clients map.
			for ip, client := range clients {
				if time.Since(client.lastSeen) > 3*time.Minute {
					delete(clients, ip)
				}
			}

			// Importantly, unlock the mutex when the cleanup is complete.
			mu.Unlock()
		}
	}()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only carry out the check if rate limited is enabled.
		if app.config.limiter.enabled {

			// ip, _, err := net.SplitHostPort(r.RemoteAddr)
			// if err != nil {
			// 	app.serverErrorResponse(w, r, err)
			// 	return
			// }

			// Use the realip.FromRequest function to get the client's real IP address.
			ip := realip.FromRequest(r)

			// Lock the mutex to prevent this code from being executed concurrently.
			mu.Lock()

			// Check to see if the IP address already exists in the map. If it doesn't,
			// then initialize a new rate limiter and add the IP address and limiter to the map.
			if _, found := clients[ip]; !found {
				// Use the requests-per-second and burst values from the app.config struct.
				clients[ip] = &client{
					limiter: rate.NewLimiter(rate.Limit(app.config.limiter.rps), app.config.limiter.burst)}
			}

			// Update the last seen time for the client.
			clients[ip].lastSeen = time.Now()

			// Call the limiter.Allow() method on the rate limiter for the current IP address.
			// If the request isn't allowed, unlock the mutex and send a 429 Too Many Requests
			// response.
			if !clients[ip].limiter.Allow() {
				mu.Unlock()
				app.rateLimitExceededResponse(w, r)
				return
			}

			// Very importantly, unlock the mutex before calling the next handler in the chain.
			// Notice that we DON'T use defer to unlock the mutex, as that would mean that the mutex
			// isn't unlocked until all handlers downstream of this middleware have also returned.
			mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

// we need to add the authenticate() middleware to our handler chain.
// We want to use this middleware on all requests
// By the time a request leaves our authenticate() middleware,
// there are now two possible states for the request context. Either:
// 1. The request context contains a User struct (representing a valid, authenticated, user).
// 2. Or the request context contains an AnonymousUser struct.
func (app *application) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add the "Vary: Authorization" header to the response.
		// This indicates to any caches that the response may vary based
		// on the value of the Authorization header in the request.
		w.Header().Set("Vary", "Authorization")

		// Retrieve the value of the Authorization header from teh request.
		// This will return the empty string "" if there is no such header found.
		authorizationHeader := r.Header.Get("Authorization")

		// If there is no Authorization header found, use the contextSetUser() helper to add
		// an AnonymousUser to the request context. Then we call the next handler in the chain
		// and return without executing any of the code below.
		if authorizationHeader == "" {
			r = app.contextSetUser(r, data.AnonymousUser)
			next.ServeHTTP(w, r)
			return
		}

		// Otherwise there is an Authorization header present.
		// If the Authorization header is provided, but it’s malformed or contains
		// an invalid value, the client will be sent a 401 Unauthorized response:

		// Otherwise, we expect the value of the Authorization header to be in the format
		// "Bearer <token>". We try to split this into its constituent parts, and if the header
		// isn't in the expected format we return a 401 Unauthorized response using the
		// invalidAuthenticationTokenResponse helper.
		headerParts := strings.Split(authorizationHeader, " ")
		if len(headerParts) != 2 || headerParts[0] != "Bearer" {
			app.invalidAuthenticationTokenResponse(w, r)
			return
		}

		// Extract the actual authentication toekn from the header parts
		token := headerParts[1]

		// Validate the token to make sure it is in a sensible format.
		v := validator.New()

		// If the token isn't valid, use the invalidAuthenticationtokenResponse
		// helper to send a response, rather than the failedValidatedResponse helper.
		if data.ValidateTokenPlaintext(v, token); !v.Valid() {
			app.invalidAuthenticationTokenResponse(w, r)
			return
		}

		// Retrieve the details of the user associated with the authentication token.
		// call invalidAuthenticationTokenResponse if no matching record was found.
		// IMPORTANT: Notice that we are using ScopeAuthentication as the
		// first parameter here.
		user, err := app.models.Users.GetForToken(data.ScopeAuthentication, token)
		if err != nil {
			switch {
			case errors.Is(err, data.ErrRecordNotFound):
				app.invalidAuthenticationTokenResponse(w, r)
			default:
				app.serverErrorResponse(w, r, err)
			}
			return
		}

		// Call the contextSetUser helper to add the user information to the request context.
		r = app.contextSetUser(r, user)

		// Call next handler in chain
		next.ServeHTTP(w, r)
	})
}

/*
 A 401 Unauthorized response should be used when you have missing or bad authentication,
 and a 403 Forbidden response should be used afterwards, when the user is authenticated
 but isn't allowed to perform the requested operation.
*/

// requireAuthenticatedUser checks that the user is not anonymous
// (i.e., they are authenticated). This middleware only cares about if the
// user is anonymous or not (i.e authenticated or not) and doesn't care about
// the active status of user, whether user's account is activated or not.
func (app *application) requireAuthenticatedUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Use the contextGetUser helper to retrieve the user information
		// from the request context.
		user := app.contextGetUser(r)

		// If the user is anonymous, then call authenticationRequiredResponse
		// to inform the client that they should be authenticated before trying again.
		if user.IsAnonymous() {
			app.authenticationRequiredResponse(w, r)
			return
		}

		next.ServeHTTP(w, r)
	}
}

// requiredActivatedUser checks that the user is both authenticated and activated.
func (app *application) requireActivatedUser(next http.HandlerFunc) http.HandlerFunc {
	// Rather than returning this http.HandlerFunc we assign it to the variable fn.
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use the contextGetUser() helper that we made earlier to retrieve the user
		// information from the request context.
		user := app.contextGetUser(r)

		// Check that a user is activated
		if !user.Activated {
			app.inactiveAccountResponse(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})

	// ** Compose or use a middleware inside another middleware.
	// Wrap fn with the requireAuthenticatedUser middleware before returning it.
	return app.requireAuthenticatedUser(fn)
}

// Note that the first parameter for the middleware function is the
// permission code that we require the user to have.
func (app *application) requirePermissions(code string, next http.HandlerFunc) http.HandlerFunc {
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Retrieve the user from the request context.
		user := app.contextGetUser(r)

		// Get the slice of permission for the user
		permissions, err := app.models.Permissions.GetAllForUser(user.ID)
		if err != nil {
			app.serverErrorResponse(w, r, err)
			return
		}

		// Check if the slice includes the required permission. If it doesn't, then return a 403
		// Forbidden response.
		if !permissions.Include(code) {
			app.notPermittedResponse(w, r)
			return
		}

		// Otherwise, they have the required permission so we call the next handler in the chain.
		next.ServeHTTP(w, r)
	})

	// Wrap this with the requireActivatedUser middleware before returning
	return app.requireActivatedUser(fn)
}

// enableCORS sets the Vary: Origin and Access-Control-Allow-Origin response headers in order to
// enabled CORS for trusted origins.
func (app *application) enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// The response will be different depending on the origin that the request
		// is coming from. This means that the response can't be cached by a shared cache
		// (e.g. a CDN) and must be revalidated each time. We can indicate this by setting
		// the "Vary: Origin" header in the response. This tells any caches that the response
		// may vary based on the value of the Origin header in the request.

		/*
			* As a rule of thumb:
			If your code makes a decision about what to return based on the content of a
			request header, you should include that header name in your Vary response
			header — even if the request didn’t include that header.
		*/

		// Add the "Vary: Origin" header.
		w.Header().Set("Vary", "Origin")

		// Add the "Vary: Access-Control-Request-Method" header.
		w.Header().Set("Vary", "Access-Control-Request-Method")

		// Get the value of the request's Origin header.
		origin := r.Header.Get("Origin")

		/*
			One of the problems is that — in practice — you can only specify exactly one
			origin in the Access-Control-Allow-Origin header. You can’t include a list of
			multiple origin values, separated by spaces or commas like you might expect.
		*/

		// On run this if there's an Origin request header present.
		if origin != "" {
			// Loop through the list of trusted origins, checking to see if the request
			// origin exactly matches one of them. If there are no trusted origins, then the
			// loop won't be iterated.
			for i := range app.config.cors.trustedOrigins {
				if origin == app.config.cors.trustedOrigins[i] {
					// If there is a match, then set an "Access-Control-Allow-Origin" response
					// header with the request origin as the value and break out of the loop.
					w.Header().Set("Access-Control-Allow-Origin", origin)

					// Check if the request is a preflight request
					// Check if the request has the HTTP method OPTIONS and contains the
					// "Access-Control-Request-Method" header. If it does, then we treat it as a
					// preflight request.
					// The preflight requests always have three components:
					// the HTTP method OPTIONS , an Origin header, and an
					// Access-Control-Request-Method header.
					if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
						// Set the necessary preflight response headers.
						w.Header().Set("Access-Control-Allow-Methods", "OPTIONS, PUT, PATCH, DELETE")
						w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")

						// Set max cached times for headers for 60 seconds.
						w.Header().Set("Access-Control-Max-Age", "60")

						// Write the headers along with a 200 OK status and return from the
						// middleware with no further action.
						w.WriteHeader(http.StatusOK)
						return
					}

					break
				}
			}
		}

		next.ServeHTTP(w, r)

		/*
			* Authentication and CORS

			If your API endpoint requires credentials (cookies or HTTP basic authentication)
			you should also set an Access-Control-Allow-Credentials: true header in your responses.

			If you don’t set this header, then the web browser will prevent any cross-origin
			responses with credentials from being read by JavaScript.

			Importantly, you must never use the wildcard Access-Control-Allow-Origin: * header
			in conjunction withAccess-Control-Allow-Credentials: true, as this would allow any
			website to make a credentialed cross-origin request to your API.

			Also, importantly, if you want credentials to be sent with a cross-origin request
			then you’ll need to explicitly specify this in your JavaScript.
			For example, with fetch() you should set the credentials value of
			the request to 'include'. Like so:
			fetch("https://api.example.com", {credentials: 'include'}).then( ... );
		*/
	})
}

func (app *application) metrics(next http.Handler) http.Handler {
	// Initialize the new expvar variables when middleware chain is first build.
	// This runs only once when the application starts up.
	totalRequestsReceived := expvar.NewInt("total_requests_received")
	totalResponsesSent := expvar.NewInt("total_responses_sent")
	totalProcessingTimeMicroseconds := expvar.NewInt("total_processing_time_µs")
	// expvar.NewMap will give us a map in which we can store the different
	//  HTTP status codes, along with a running count of responses for each status.
	totalResponsesSentbyStatus := expvar.NewMap("total_responses_sent_by_status")

	// The number of ‘active’ in-flight requests:
	// totalInflightActiveRequests := totalRequestsReceived - totalResponsesSent
	// Average processing time per request:
	// averageProcessingTime := totalProcessingTimeMicroseconds / totalResponsesSent

	// Below runs for every request.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// use the Add method to increment the number of requests received by 1.
		totalRequestsReceived.Add(1)

		// Call the httpsnoop.CaptureMetrics function, passing in the next handler in the chain
		// along with the existing http.ResponseWriter and http.Request.
		// This returns the Metrics struct.
		type Metrics struct {
			// Code is the first http response status code passed to the WriteHeader() method of
			// the ResponseWriter. If no such call is made, a default code of 200 is
			// assumed instead.
			Code int
			// Duration is the time it took to execute the handler.
			Duration time.Duration
			// Written is the number of bytes successfully written by the Write() method of the
			// ResponseWriter. Note that ResponseWriters may also write data to their underlying
			// connection directly, but those writes are not tracked.
			Written int64
		}

		metrics := httpsnoop.CaptureMetrics(next, w, r)

		// On way back up middleware chain:

		// Increment the number of responses sent by 1.
		totalResponsesSent.Add(1)

		// Get the request processing time in microseconds from httpsnoop
		// and increment the cumulative processing time.
		totalProcessingTimeMicroseconds.Add(metrics.Duration.Microseconds())

		// Use the Add method to increment the count for the given status code by 1.
		// Note, the expvar map is string-keyed, so we need to use the strconv.Itoa
		// function to convert the status (an integer) to a string.
		totalResponsesSentbyStatus.Add(strconv.Itoa(metrics.Code), 1)
	})
}

// Without using httpsnoop package
func (app *application) metrics2(next http.Handler) http.Handler {
	// Initialize the new expvar variables when the middleware chain is first built.
	totalRequestsReceived := expvar.NewInt("total_requests_received")
	totalResponsesSent := expvar.NewInt("total_responses_sent")
	totalProcessingTimeMicroseconds := expvar.NewInt("total_processing_time_μs")
	// The following code will be run for every request...
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record the time that we started to process the request.
		start := time.Now()
		// Use the Add() method to increment the number of requests received by 1.
		totalRequestsReceived.Add(1)
		// Call the next handler in the chain.
		next.ServeHTTP(w, r)

		// On the way back up the middleware chain:

		// Increment the number of responses // sent by 1.
		totalResponsesSent.Add(1)
		// Calculate the number of microseconds since we began to process the request, // then increment the total processing time by this amount.
		duration := time.Since(start).Microseconds()
		totalProcessingTimeMicroseconds.Add(duration)
	})
}

// If you only have a couple of endpoints where you want to perform authorization checks,
// then rather than using middleware it can often be easier to do the checks inside
// the relevant handlers instead.
// For example:
func (app *application) exampleHandler(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)
	if user.IsAnonymous() {
		app.authenticationRequiredResponse(w, r)
		return
	}
	if !user.Activated {
		app.inactiveAccountResponse(w, r)
		return
	}
	// The rest of the handler logic goes here...
}

/*

* Preflight CORS Requests *

Broadly speaking, cross-origin requests are classified as
‘simple cross origin requests’ when all the following conditions are met:

The request HTTP method is one of the three CORS-safe methods: HEAD, GET or POST.
The request headers are all either forbidden headers or one of the four CORS-safe
headers:
     Accept
     Accept-Language
     Content-Language
     Content-Type

The value for the Content-Type header (if set) is one of:
   application/x-www-form-urlencoded
   multipart/form-data
   text/plain

When a cross-origin request doesn’t meet these conditions, then the web browser
will trigger an initial ‘preflight’ request before the real request.
The purpose of this preflight request is to determine whether the real
cross-origin request will be permitted or not.

For example if we set Content-Type: application/json in request headers, then
it's not a 'simple cross origin request' and the browser will have to make a
'preflight request'.

There are three headers which are relevant to CORS during a preflight request:

1. Origin — This lets our API know what origin the preflight request is coming from.
2. Access-Control-Request-Method — This lets our API know what HTTP method will be
  used for the real request (in this case, we can see that the real request will be a POST).
3. Access-Control-Request-Headers — This lets our API know what HTTP headers will be sent
   with the real request.

It’s important to note that Access-Control-Request-Headers won’t list all the headers
that the real request will use. Only headers that are not CORS-safe or forbidden will be listed.
If there are no such headers, then Access-Control-Request-Headers may be omitted from the
preflight request entirely.


Responding to preflight requests

In order to respond to a preflight request, the first thing we need to do is identify
that it is a preflight request — rather than just a
regular (possibly even cross-origin) OPTIONS request.

To do that, we can leverage the fact that "preflight requests always have
three components: the HTTP method OPTIONS, an Origin header, and
an Access-Control-Request-Method header". If any one of these pieces is missing,
we know that it is not a preflight request.

Once we identify that it is a preflight request, we need to send a 200 OK response
with some special headers to let the browser know whether or not it’s OK for the
real request to proceed. These are:
1. An Access-Control-Allow-Origin response header, which reflects the value of the
   preflight request’s Origin header
2. An Access-Control-Allow-Methods header listing the HTTP methods that can be
   used in real cross-origin requests to the URL.
3. An Access-Control-Allow-Headers header listing the request headers that can
   be included in real cross-origin requests to the URL.


When the web browser receives these headers, it compares the values of the response
headers to the method and (case-insensitive) headers that it wants to use in the real request.
If the method or any of the headers are not allowed, then the browser will block
the real request.


If you want, you can also add an Access-Control-Max-Age header to your preflight
responses. This indicates the number of seconds that the information provided by the Access-Control-Allow-Methods and Access-Control-Allow-Headers headers can be cached
by the browser.

Setting a long Access-Control-Max-Age duration might seem like an appealing way
to reduce requests to your API — and it is!

If you want to disable caching altogether, you can set the value to -1 :
Access-Control-Max-Age: -1


You might think: I just want to allow all HTTP methods and headers for cross-origin requests.
In this case, both the Access-Control-Allow-Methods and Access-Control-Allow-Headers
headers allow you to use a wildcard * character like so:

Access-Control-Allow-Methods: *
Access-Control-Allow-Headers: *

But using these comes with some important caveats:

1. Wildcards in these headers are currently only supported by 74% of browsers.
   Any browsers which don’t support them will block the preflight request.
2. The Authorization header cannot be wildcarded. Instead, you will need to include this
   explicitly in the header like this - Access-Control-Allow-Headers: Authorization, *.
3. Wildcards are not supported for credentialed requests (those with cookies or HTTP
	basic authentication). For these, the character * will be treated as the literal
	string "*", rather than as a wildcard.

*/
