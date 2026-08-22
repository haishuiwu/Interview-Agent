package dashscope

type realtimeServerEvent struct {
	Type       string `json:"type"`
	EventID    string `json:"event_id"`
	Text       string `json:"text"`
	Stash      string `json:"stash"`
	Transcript string `json:"transcript"`
	Session    struct {
		ID string `json:"id"`
	} `json:"session"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

type realtimeWriteCommand struct {
	audio  []byte
	finish bool
	result chan error
}
