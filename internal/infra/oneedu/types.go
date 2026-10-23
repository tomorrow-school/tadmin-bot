package oneedu

import "time"

// --- GraphQL response types (match the real 01-edu API schema) ---

// piscineResponse is the response for GetCurrentPiscineId.
type piscineResponse struct {
	Data struct {
		Event []piscineEventNode `json:"event"`
	} `json:"data"`
}

type piscineEventNode struct {
	ID      int       `json:"id"`
	StartAt time.Time `json:"startAt"`
	EndAt   time.Time `json:"endAt"`
}

// piscinesResponse is the response for GetCurrentPiscinesId / GetUpcomingPiscinesId.
type piscinesResponse struct {
	Data struct {
		Event []piscineListEventNode `json:"event"`
	} `json:"data"`
}

type piscineListEventNode struct {
	ID      int       `json:"id"`
	StartAt time.Time `json:"startAt"`
	EndAt   time.Time `json:"endAt"`
	Path    string    `json:"path"`
}

// registrationCountResponse is the response for GetRegistrationCountByPath.
type registrationCountResponse struct {
	Data struct {
		Registrations aggregateCountNode `json:"registrations"`
	} `json:"data"`
}

// participantCountResponse is the response for GetParticipantCountByPath.
type participantCountResponse struct {
	Data struct {
		Participants aggregateCountNode `json:"participants"`
	} `json:"data"`
}

// raidsResponse is the response for GetRaidsByPiscine*Id and GetRaidByName.
type raidsResponse struct {
	Data struct {
		Event []raidEventNode `json:"event"`
	} `json:"data"`
}

type raidEventNode struct {
	ID       int         `json:"id"`
	Object   objectNode  `json:"object"`
	ParentID int         `json:"parentId"`
	Groups   []groupNode `json:"groups"`
	StartAt  time.Time   `json:"startAt"`
	EndAt    time.Time   `json:"endAt"`
}

type objectNode struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type groupNode struct {
	Captain     captainNode     `json:"captain"`
	GroupStatus groupStatusNode `json:"group_status"`
	Members     []memberNode    `json:"members"`
}

type captainNode struct {
	Login string `json:"login"`
}

type groupStatusNode struct {
	Status string `json:"status"`
}

type memberNode struct {
	ID        int    `json:"id"`
	UserLogin string `json:"userLogin"`
}

type aggregateCountNode struct {
	Aggregate struct {
		Count int `json:"count"`
	} `json:"aggregate"`
}

type astanaUpdatesResponse struct {
	Data astanaUpdatesNode `json:"data"`
}

type astanaUpdatesNode struct {
	TotalAstana     aggregateCountNode `json:"total_astana"`
	SucceededAstana aggregateCountNode `json:"succeeded_astana"`
	CheckinAstana   aggregateCountNode `json:"checkin_astana"`
}

// eventByIDResponse is the response for GetEventByID.
type eventByIDResponse struct {
	Data struct {
		Event []eventMetaNode `json:"event"`
	} `json:"data"`
}

type eventMetaNode struct {
	ID      int        `json:"id"`
	Path    string     `json:"path"`
	StartAt time.Time  `json:"startAt"`
	EndAt   time.Time  `json:"endAt"`
	Object  objectNode `json:"object"`
}

// eventInfoResponse is the response for GetEventInfo.
type eventInfoResponse struct {
	Data struct {
		Event []eventInfoNode `json:"event"`
	} `json:"data"`
}

type eventInfoNode struct {
	ID            int                     `json:"id"`
	Path          string                  `json:"path"`
	StartAt       time.Time               `json:"startAt"`
	EndAt         time.Time               `json:"endAt"`
	Registrations []eventRegistrationNode `json:"registrations"`
}

type eventRegistrationNode struct {
	StartAt        time.Time          `json:"startAt"`
	EndAt          time.Time          `json:"endAt"`
	UsersAggregate aggregateCountNode `json:"users_aggregate"`
}

// recruitmentEventsResponse is the response for RegionCheckinPiscine.
type recruitmentEventsResponse struct {
	Data struct {
		Event []recruitmentEventNode `json:"event"`
	} `json:"data"`
}

type recruitmentEventNode struct {
	ID            int                `json:"id"`
	StartAt       time.Time          `json:"startAt"`
	EndAt         time.Time          `json:"endAt"`
	Object        objectNode         `json:"object"`
	Participants  aggregateCountNode `json:"participants"`
	Registrations []struct {
		Applications aggregateCountNode `json:"applications"`
	} `json:"registrations"`
}

type campusesResponse struct {
	Data *campusesNode `json:"data"`
}

type campusesNode struct {
	Object []campusNode `json:"object"`
}

type campusNode struct {
	Name string `json:"name"`
}

type regionUpdatesResponse struct {
	Data *regionUpdatesNode `json:"data"`
}

type regionUpdatesNode struct {
	SignedUpNoOnboarding strictAggregateCountNode `json:"signed_up_no_onboarding"`
	Succeeded            strictAggregateCountNode `json:"succeeded"`
	Checkin              strictAggregateCountNode `json:"checkin"`
	Core                 strictAggregateCountNode `json:"core"`
}

type strictAggregateCountNode struct {
	Aggregate *countNode `json:"aggregate"`
}

type countNode struct {
	Count int `json:"count"`
}
