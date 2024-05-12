package data

// Mocking models

type Models2 struct {
	// Set the Movies field to be an interface containing the methods that both the
	// 'real' model and mock model need to support.
	Movies interface {
		Insert(movie *Movie) error
		Get(id int64) (*Movie, error)
		Update(movie *Movie) error
		Delete(id int64) error
	}
}

// Create a helper function which returns a Models instance containing the mock models // only.
func NewMockModels() Models2 {
	return Models2{
		Movies: MockMovieModel{},
	}
}

type MockMovieModel struct{}

func (m MockMovieModel) Insert(movie *Movie) error {
	// Mock the action...
	return nil
}

func (m MockMovieModel) Get(id int64) (*Movie, error) {
	// Mock the action...
	return nil, nil
}

func (m MockMovieModel) Update(movie *Movie) error {
	// Mock the action...
	return nil
}

func (m MockMovieModel) Delete(id int64) error {
	// Mock the action...
	return nil
}

// You can then call NewMockModels() whenever you need it in your unit tests
// in place of the ‘real’ NewModels() function.
