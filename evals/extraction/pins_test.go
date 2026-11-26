package extraction

import (
	"maps"
	"slices"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
)

// pinnedPackets are the SHA-256s of one golden and one seeded packet's
// files and truth.json, which CI checks on both its Linux and its Windows
// job. Rendered packets aren't committed, so this is what notices a
// platform difference or a change in Go or a library that moves packet
// bytes: gopdf always compresses the embedded font subset, so a change to
// compress/flate would move them. The seeded packet prints each of the
// eight document types in a file of its own.
//
// A deliberate change to the generator gets a new datagen.Version, and
// these pins move with it, in the same commit. Never change them only to
// make CI pass: a difference between the two jobs is a bug to find.
var pinnedPackets = map[string]map[string]string{
	"golden-01-worked-example": {
		"claim-documents.pdf": "0625b8c74fda3dd73cffcf04ca23d8bd4d14b10efced4a3967e334aafc091107",
		"truth.json":          "d4b3d7f78971b2e158b9ed99c02a7ad7ef3d64de53aa186fe3b23149ddb49d19",
	},
	"seeded-surgery-implant-03": {
		"claim-form.pdf":        "1658be0bf17856a6ed55766a517b7f9566416bdb6e2db4518acf2b11dcb11402",
		"discharge-summary.pdf": "7f731d778f576812062b4af091edc54ae20367ffe5a705229516d95dd8827d26",
		"hospital-bill.pdf":     "44abac72cdf81e1ba614dd7836491d5865ce081e0db016e2c330176775593177",
		"implant-invoice.pdf":   "63812885373d703a5c4649067b19c35ae16c7ac38d7a8a50ea212add70286b09",
		"ot-notes.pdf":          "4b2d417ca9035e2b5074771e364dbc54c16834af608126993e60b8e745823982",
		"payment-receipt.pdf":   "da94f70295ba857dd6b639d7403541a0ffa50687e13b971361b6612691aec769",
		"pharmacy-bill.pdf":     "dde3f0d1a98285fd8f0d72926c8596d2a66aebc485230f396026a7f4598f840c",
		"photo-id.pdf":          "47ce455e6f144a452d071692d54a7b5c07bead44f7e035bbd34319ddece4b164",
		"truth.json":            "727efd0906891908455e536b2f66ab20038247176d12fc5fe27a8ad68c795024",
	},
}

func TestPinnedPacketHashes(t *testing.T) {
	specs, err := Specs()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]datagen.Spec{}
	for _, s := range specs {
		byID[s.ID] = s
	}
	for _, id := range slices.Sorted(maps.Keys(pinnedPackets)) {
		t.Run(id, func(t *testing.T) {
			s, ok := byID[id]
			if !ok {
				t.Fatalf("no spec %s", id)
			}
			p, err := datagen.Render(s)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, f := range p.Files {
				got[f.Name] = sha(f.Data)
			}
			truth, err := datagen.TruthJSON(p.Truth)
			if err != nil {
				t.Fatal(err)
			}
			got["truth.json"] = sha(truth)
			want := pinnedPackets[id]
			for _, name := range slices.Sorted(maps.Keys(want)) {
				if got[name] != want[name] {
					t.Errorf("%s: SHA-256 %s, pinned %s", name, got[name], want[name])
				}
			}
			if len(got) != len(want) {
				t.Errorf("rendered %v, pinned %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
			}
		})
	}
}
