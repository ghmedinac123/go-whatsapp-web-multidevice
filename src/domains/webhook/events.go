package webhook

// SupportedEvent describes a webhook event the server can emit. Group is a UI
// grouping label and Description is a short human hint. This catalog is the
// single source of truth consumed by the GET /webhook/events endpoint and the
// per-device webhook UI, so the client can pick events instead of typing them.
// Keep it in sync with the event names emitted in
// infrastructure/whatsapp/event_*.go.
type SupportedEvent struct {
	Event       string `json:"event"`
	Group       string `json:"group"`
	Description string `json:"description"`
}

// SupportedEvents returns the catalog of webhook events a device can subscribe
// to. An empty per-device Events filter still means "all events".
func SupportedEvents() []SupportedEvent {
	return []SupportedEvent{
		{Event: "message", Group: "Message", Description: "Incoming or outgoing message"},
		{Event: "message.reaction", Group: "Message", Description: "Reaction (emoji) on a message"},
		{Event: "message.revoked", Group: "Message", Description: "Message revoked for everyone"},
		{Event: "message.edited", Group: "Message", Description: "Message edited"},
		{Event: "message.ack", Group: "Message", Description: "Delivery/read receipt"},
		{Event: "message.deleted", Group: "Message", Description: "Message deleted locally"},
		{Event: "group.participants", Group: "Group", Description: "Participants added/removed/promoted"},
		{Event: "group.joined", Group: "Group", Description: "This device joined a group"},
		{Event: "newsletter.joined", Group: "Newsletter", Description: "Joined a newsletter/channel"},
		{Event: "newsletter.left", Group: "Newsletter", Description: "Left a newsletter/channel"},
		{Event: "newsletter.message", Group: "Newsletter", Description: "New newsletter/channel message"},
		{Event: "newsletter.mute", Group: "Newsletter", Description: "Newsletter mute state changed"},
		{Event: "call.offer", Group: "Call", Description: "Incoming call offer"},
		{Event: "chat_presence", Group: "Presence", Description: "Typing/recording presence in a chat"},
		{Event: "label.edit", Group: "Label", Description: "Label created or edited"},
		{Event: "label.association", Group: "Label", Description: "Label assigned to or removed from a chat"},
	}
}
