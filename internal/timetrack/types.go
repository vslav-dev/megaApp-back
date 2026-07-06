package timetrack

type Track string

const (
	TrackPrimary   Track = "primary"
	TrackSecondary Track = "secondary"
)

type Impact string

const (
	ImpactUseful   Impact = "useful"
	ImpactNeutral  Impact = "neutral"
	ImpactWasteful Impact = "wasteful"
)

const CategoryKindArea = "area"

type Activity struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	IsArchived  bool    `json:"isArchived"`
	CategoryIDs []int64 `json:"categoryIds"`
	CreatedAt   string  `json:"createdAt"`
}

type Category struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Kind      *string `json:"kind"`
	Color     *string `json:"color"`
	Impact    *string `json:"impact"`
	CreatedAt string  `json:"createdAt"`
}

type Entry struct {
	ID         int64  `json:"id"`
	ActivityID int64  `json:"activityId"`
	Track      Track  `json:"track"`
	StartAt    string `json:"startAt"`
	EndAt      string `json:"endAt"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type ActivityInput struct {
	Name        string  `json:"name"`
	IsArchived  bool    `json:"isArchived"`
	CategoryIDs []int64 `json:"categoryIds"`
}

type CategoryInput struct {
	Name   string  `json:"name"`
	Kind   *string `json:"kind"`
	Color  *string `json:"color"`
	Impact *string `json:"impact"`
}

type EntryInput struct {
	ActivityID int64  `json:"activityId"`
	Track      Track  `json:"track"`
	StartAt    string `json:"startAt"`
	EndAt      string `json:"endAt"`
}
