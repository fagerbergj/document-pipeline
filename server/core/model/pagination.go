package model

// PageToken is the decoded form of a cursor passed between API requests.
type PageToken struct {
	SortKey string `json:"k"`
	LastID  string `json:"id"`
}

// PageResult is a generic paginated response.
type PageResult[T any] struct {
	Data          []T
	NextPageToken *string // nil when there are no more pages
}

// PageRequest carries pagination parameters into repository calls.
type PageRequest struct {
	PageSize  int
	PageToken *PageToken
}
