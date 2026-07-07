package timetrack

type Track string

const (
	TrackPrimary   Track = "primary"
	TrackSecondary Track = "secondary"
)

const CategoryGroupKindArea = "area"

type GroupBinding struct {
	GroupID  int64 `json:"groupId"`
	Required bool  `json:"required"`
}

type ActivityKind struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	GroupBindings []GroupBinding `json:"groupBindings"`
	IsArchived    bool           `json:"isArchived"`
	CreatedAt     string         `json:"createdAt"`
	UpdatedAt     string         `json:"updatedAt"`
}

type CategoryGroup struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Kind       *string `json:"kind"`
	IsArchived bool    `json:"isArchived"`
	CreatedAt  string  `json:"createdAt"`
	UpdatedAt  string  `json:"updatedAt"`
}

type CategoryOption struct {
	ID         int64   `json:"id"`
	GroupID    int64   `json:"groupId"`
	Name       string  `json:"name"`
	Color      *string `json:"color"`
	IsArchived bool    `json:"isArchived"`
	CreatedAt  string  `json:"createdAt"`
	UpdatedAt  string  `json:"updatedAt"`
}

type EntryOption struct {
	GroupID  int64 `json:"groupId"`
	OptionID int64 `json:"optionId"`
}

type Entry struct {
	ID             int64         `json:"id"`
	ActivityKindID int64         `json:"activityKindId"`
	Track          Track         `json:"track"`
	StartAt        string        `json:"startAt"`
	EndAt          string        `json:"endAt"`
	Options        []EntryOption `json:"options"`
	CreatedAt      string        `json:"createdAt"`
	UpdatedAt      string        `json:"updatedAt"`
}

type Catalog struct {
	ActivityKinds   []ActivityKind   `json:"activityKinds"`
	CategoryGroups  []CategoryGroup  `json:"categoryGroups"`
	CategoryOptions []CategoryOption `json:"categoryOptions"`
}

type ActivityKindInput struct {
	Name          string         `json:"name"`
	GroupBindings []GroupBinding `json:"groupBindings"`
	IsArchived    bool           `json:"isArchived"`
}

type CategoryGroupInput struct {
	Name       string  `json:"name"`
	Kind       *string `json:"kind"`
	IsArchived bool    `json:"isArchived"`
}

type CategoryOptionInput struct {
	GroupID    int64   `json:"groupId"`
	Name       string  `json:"name"`
	Color      *string `json:"color"`
	IsArchived bool    `json:"isArchived"`
}

type EntryInput struct {
	ActivityKindID int64         `json:"activityKindId"`
	Track          Track         `json:"track"`
	StartAt        string        `json:"startAt"`
	EndAt          string        `json:"endAt"`
	Options        []EntryOption `json:"options"`
}

type EntryTimeInput struct {
	Track   Track  `json:"track"`
	StartAt string `json:"startAt"`
	EndAt   string `json:"endAt"`
}

type EntrySelectionInput struct {
	ActivityKindID int64         `json:"activityKindId"`
	Options        []EntryOption `json:"options"`
}
