package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidRuntimeFormat returns error when we are unable to parse or convert a JSON string
// successfully. This is used in our Runtime.UnmarshalJSON() method.
var ErrInvalidRuntimeFormat = errors.New("invalid runtime format")

type Runtime int32

// MarshalJSON method on the Runtime type so that it satisfies the
// json.Marshaler interface. This should return the JSON-encoded string for the movie
// runtime in the format "<runtime> mins".

// We’re deliberately using a value receiver for our MarshalJSON()
// method rather than a pointerreceiverlikefunc (r *Runtime) MarshalJSON().
// This gives us more flexibility because it means that our custom JSON encoding
// will work on both Runtime values and pointers to Runtime values.
func (r Runtime) MarshalJSON() ([]byte, error) {
	// Generate a string containing the movie runtime in the required format
	jsonValue := fmt.Sprintf("%d mins", r)

	// Use the strconv.Quote() function on the string to wrap it double quotes.
	// It needs to be surrounded by double quotes in order to be a valid JSON string.
	quotedJSONValue := strconv.Quote(jsonValue)

	// Convert the quoted string value to a byte slice and return it
	return []byte(quotedJSONValue), nil
}

// ** Alternative - Customizing the Movie struct
// Instead of creating a custom Runtime type, we could have implemented a
// MarshalJSON() method on our Movie struct and customized the whole thing. Like this:

// Notice that we use the - directive on the Runtime field,
// so that it never appears in the JSON output.
type Movie2 struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"-"`
	Title     string    `json:"title"`
	Year      int32     `json:"year,omitempty"`
	Runtime   int32     `json:"-"`
	Genres    []string  `json:"genres,omitempty"`
	Version   int32     `json:"version"`
}

func (m Movie2) MarshalJSON() ([]byte, error) {
	// Create a variable holding the custom runtime string, just like before.
	var runtime string

	if m.Runtime != 0 {
		runtime = fmt.Sprintf("%d mins", m.Runtime)
	}
	// Define a MovieAlias type which has the underlying type Movie. Due to the way that
	// Go handles type definitions (https://golang.org/ref/spec#Type_definitions) the
	// MovieAlias type will contain all the fields that our Movie struct has but,
	// importantly, none of the methods.
	type MovieAlias Movie2

	// Embed the MovieAlias type inside the anonymous struct, along with a Runtime field
	// that has the type string and the necessary struct tags. It's important that we
	// embed the MovieAlias type here, rather than the Movie type directly, to avoid
	// inheriting the MarshalJSON() method of the Movie type (which would result in an
	// infinite loop during encoding).
	aux := struct {
		MovieAlias
		Runtime string `json:"runtime,omitempty"`
	}{
		MovieAlias: MovieAlias(m),
		Runtime:    runtime,
	}
	return json.Marshal(aux)
}

// UnmarshalJSON ensures that Runtime satisfies the
// json.Unmarshaler interface. IMPORTANT: because UnmarshalJSON() needs to modify the
// receiver (our Runtime type), we must use a pointer receiver for this to work
// correctly. Otherwise, we will only be modifying a copy (which is then discarded when
// this method returns).
func (r *Runtime) UnmarshalJSON(jsonValue []byte) error {
	// We expect that the incoming JSON value will be a string in the format
	// "<runtime> mins", and the first thing we need to do is remove the surrounding
	// double-quotes from this string. If we can't unquote it, then we return the
	// ErrInvalidRuntimeFormat error.
	unquotedJSONValue, err := strconv.Unquote(string(jsonValue))
	if err != nil {
		return ErrInvalidRuntimeFormat
	}

	// Split the string to isolate the part containing the number.
	parts := strings.Split(unquotedJSONValue, " ")

	// Sanity check the parts of the string to make sure it was in the expected format.
	// If it wasn't, we return the ErrInvalidRuntimeFormat error again.
	if len(parts) != 2 || parts[1] != "mins" {
		return ErrInvalidRuntimeFormat
	}

	// Otherwise, parse the string containing the number into an int32. Again, if this
	// fails return the ErrInvalidRuntimeFormat error.
	i, err := strconv.ParseInt(parts[0], 10, 32)
	if err != nil {
		return ErrInvalidRuntimeFormat
	}

	// Convert the int32 to a Runtime type and assign this to the receiver. Note that we
	// use the * operator to deference the receiver (which is a pointer to a Runtime
	// type) in order to set the underlying value of the pointer.
	*r = Runtime(i)

	return nil
}
