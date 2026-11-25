package datagen

// AnnexureItem is one item of Annexure A in the wording, as printed there.
type AnnexureItem struct {
	// List is the engine's name for the list, as facts' item_list holds it.
	List string
	// Number is the item's number within its list.
	Number int
	// Name is the item as printed in the wording, without the note in
	// brackets after it.
	Name string
	// Page is the wording's page the item is printed on.
	Page int
}

// Key is how a spec names the item: "list-ii/27".
func (a AnnexureItem) Key() string { return a.List + "/" + itoa(int64(a.Number)) }

// annexureItems is a short table of Annexure A items, enough for the
// packets' listed lines. Each name is copied from the 25-page Go Digit
// wording the rule pack cites (SHA-256 70ec4e51…a75b, pages 20 to 24). The
// full transcription waits for M3, and the published rule pack doesn't
// change.
//
// In Lists III to V the wording's text layer prints the item numbers apart
// from the names, so those numbers are counted in the order the names are
// printed.
var annexureItems = []AnnexureItem{
	// List I, "Optional Items", pages 20 and 21.
	{"list-i", 11, "LAUNDRY CHARGES", 20},
	{"list-i", 14, "TELEPHONE CHARGES", 20},
	{"list-i", 28, "COURIER CHARGES", 20},
	{"list-i", 32, "PHOTOCOPIES CHARGES", 20},
	{"list-i", 41, "THERMOMETER", 20},
	// List II, "Items that are to be subsumed into Room Charges", pages 21
	// (items 1 to 23) and 22.
	{"list-ii", 9, "GOWN", 21},
	{"list-ii", 11, "TISSUE PAPER", 21},
	{"list-ii", 13, "TOOTHBRUSH", 21},
	{"list-ii", 27, "ADMISSION KIT", 22},
	{"list-ii", 30, "DISCHARGE PROCEDURE CHARGES", 22},
	// List III, "Items that are to be subsumed into Procedure Charges",
	// page 22.
	{"list-iii", 3, "EYE PAD", 22},
	{"list-iii", 8, "GAUZE", 22},
	{"list-iii", 15, "EYE DRAPE", 22},
	{"list-iii", 18, "COTTON", 22},
	{"list-iii", 21, "APRON", 22},
	// List IV, "Items that are to be subsumed into costs of treatment",
	// headed on page 22 with every item on page 23.
	{"list-iv", 1, "ADMISSION/REGISTRATION CHARGES", 23},
	{"list-iv", 3, "URINE CONTAINER", 23},
	{"list-iv", 7, "INFUSION PUMP- COST", 23},
	{"list-iv", 15, "ALCOHOL SWABES", 23},
	{"list-iv", 18, "URINE BAG", 23},
	// List V, "Additional Non Payable Items", pages 23 (items 1 to 27)
	// and 24.
	{"list-v", 5, "Barber Charges", 23},
	{"list-v", 17, "Medicine Box", 23},
	{"list-v", 25, "Examination Gloves", 23},
	{"list-v", 31, "Stockings", 24},
}

// AnnexureItems returns the table, in list and item order.
func AnnexureItems() []AnnexureItem { return append([]AnnexureItem(nil), annexureItems...) }

func annexureItem(key string) (AnnexureItem, bool) {
	for _, a := range annexureItems {
		if a.Key() == key {
			return a, true
		}
	}
	return AnnexureItem{}, false
}
