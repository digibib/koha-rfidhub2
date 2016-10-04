package main

// Message is a message to or from Koha's user interface.
type Message struct {
	Action       string // CHECKIN/CHECKOUT/CONNECT/ITEM-INFO/RETRY-ALARM-ON/RETRY-ALARM-OFF/WRITE/END
	Patron       string // Patron username/barcode
	Branch       string // branch where transaction is taking place
	RFIDError    bool   // true if RFID-reader is unavailable
	SIPError     bool   // true if SIP-server is unavailable
	UserError    bool   // true if user is not using the API correctly
	ErrorMessage string // textual description of the error
	Item         item   // current item in focus (checked in, out etc.)
}

type item struct {
	Biblionr   string
	Borrowernr string
	Label      string
	Barcode    string
	Date       string // Format: 10/03/2013
	Status     string // An error explanation or an error message passed on from SIP-server
	Transfer   string // Branchcode, or empty string if item belongs to the issuing branch
	Hold       bool   // true if item is reserved for the current branch
	NumTags    int

	// Possible errors
	Unknown           bool // true if SIP server cant give any information on a given barcode
	TransactionFailed bool // true if the transaction failed
	AlarmOnFailed     bool // true if it failed to turn on alarm
	AlarmOffFailed    bool // true if it failed to turn off alarm
	WriteFailed       bool // true if write to tag failed
	TagCountFailed    bool // true if mismatch between expected number of tags and found tags
}
