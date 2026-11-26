// Package e2eseed fills a new, empty data dir with synthetic works and test accounts, for the
// web UI's Playwright tests (`ao3mirror dev ui-server`, docs/part4-ui.md stage 4).
//
// Every string here is lorem ipsum or an obvious fixture name. Never put real AO3 content here
// (CLAUDE.md "HARD RULE"). Like the Go tests' seeds, it writes rows with store calls directly:
// it is test data, not business logic, so the controllers' layering rule doesn't apply.
package e2eseed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	rootdirs "github.com/rmb938/personal-aO3-mirror/internal/dirs" // "dirs" is the Dirs parameter
	"github.com/rmb938/personal-aO3-mirror/internal/imagefetch"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Dirs are where serve reads each part of the data dir.
type Dirs struct {
	Data     *os.Root // data_dir, opened as a root: meta.db and content.db
	Images   string   // the image worker's directory, relative to Data
	Accounts string   // users.db and keys.db (the admin socket's directory), relative to Data
}

// Account is a seeded user's credentials. They are fixtures, not secrets: the server they
// sign in to holds only synthetic data and lives as long as one test run.
type Account struct {
	Username      string   `json:"username"`
	Password      string   `json:"password"`
	TOTPSecret    string   `json:"totp_secret"` // base32, as an authenticator app takes it
	RecoveryCodes []string `json:"recovery_codes"`
}

// Seeded is what Seed wrote: the accounts (by Accounts' keys) and the IDs tests can open.
type Seeded struct {
	Accounts map[string]Account `json:"accounts"`
	Works    []int64            `json:"works"`
	Series   []int64            `json:"series"`
}

// Accounts are the seeded accounts' keys in Seeded.Accounts, each for one group of tests, so a
// test that changes an account (its password, recovery codes, sessions, login backoff, last
// TOTP step) never disturbs another group. Each username is "e2e_" plus the key.
var Accounts = []string{
	"reader",      // signing in and out (web/tests/login.spec.ts)
	"sessions",    // the account page's sessions list
	"password",    // changing the password
	"recovery",    // replacing the recovery codes
	"screenshots", // the UI screenshots for pull requests (web/tests/screenshots.spec.ts)
}

// The seeded IDs. Tests read them from Seeded rather than repeating them.
const (
	WorkChaptered int64 = 1001 // 3 of 4 chapters, notes everywhere, a skin, images; series part 1
	WorkOneShot   int64 = 1002 // one chapter; series part 3
	WorkAnonymous int64 = 1003 // anonymous, no notes, no series
	WorkHostile   int64 = 1004 // two creators, a collection; its chapter 2 and skin carry XSS payloads
	SeriesPartial int64 = 2001 // 4 parts: 1 and 3 mirrored, 2 known but not fetched, 4 unknown
	seriesMissing int64 = 1501 // part 2 of SeriesPartial, never fetched
)

// Password is every seeded account's password. It passes auth.CheckPassword.
const Password = "fixture-e2e-password-lorem" //nolint:gosec // a fixture for a throwaway synthetic server, not a secret

// Username is the seeded account's username for an Accounts key.
func Username(key string) string { return "e2e_" + key }

// The images WorkChaptered embeds: a PNG and an SVG (with a script that must never run) that
// are downloaded, and a third still queued, so the UI has a not-mirrored image to handle.
const (
	imagePNG    = "https://img.example.test/fixture-square.png"
	imageSVG    = "https://img.example.test/fixture-shape.svg"
	imageQueued = "https://img.example.test/fixture-queued.png"
)

// fixtureSVG runs script if a browser ever renders it as a document. Served as an <img> (the
// only way the UI uses images), it can't.
const fixtureSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64">` +
	`<rect width="64" height="64" fill="#2a6f97"/><circle cx="32" cy="32" r="20" fill="#f4d35e"/>` +
	`<script>window.__ao3mirrorSVGRan = true;</script></svg>`

// cheapArgon2 keeps seeding fast. The hash records its parameters, so the server checks it.
var cheapArgon2 = auth.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

// Seed writes the fixtures. The data dir must be empty: Seed refuses one that has anything in
// it, so it can never write into a real data dir. The image and accounts directories are made
// inside it.
func Seed(ctx context.Context, dirs Dirs, log *slog.Logger) (Seeded, error) {
	if err := requireEmpty(dirs.Data); err != nil {
		return Seeded{}, err
	}
	now := time.Now()
	if err := seedContent(ctx, dirs, log, now); err != nil {
		return Seeded{}, fmt.Errorf("seed content: %w", err)
	}
	accs, err := seedAccounts(ctx, dirs, log, now)
	if err != nil {
		return Seeded{}, fmt.Errorf("seed accounts: %w", err)
	}
	return Seeded{
		Accounts: accs,
		Works:    []int64{WorkChaptered, WorkOneShot, WorkAnonymous, WorkHostile},
		Series:   []int64{SeriesPartial},
	}, nil
}

// requireEmpty accepts an empty directory.
func requireEmpty(dir *os.Root) (err error) {
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	entries, err := f.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty: e2eseed only writes into a new directory", dir.Name())
	}
	return nil
}

// seedContent writes the works, the series and the images.
func seedContent(ctx context.Context, dirs Dirs, log *slog.Logger, now time.Time) (err error) {
	st, err := store.Open(ctx, dirs.Data, store.Options{Logger: log})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()
	imageDir, err := rootdirs.Sub(dirs.Data, dirs.Images, 0o750)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, imageDir.Close()) }()

	pngBody, err := fixturePNG()
	if err != nil {
		return err
	}
	images := map[string]storedImage{
		imagePNG: {body: pngBody, contentType: "image/png"},
		imageSVG: {body: []byte(fixtureSVG), contentType: "image/svg+xml"},
	}
	for _, img := range images {
		if err := img.write(imageDir); err != nil {
			return err
		}
	}
	return st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		for _, w := range works() {
			if _, err := tx.PutWorkContent(ctx, w.ID, w.Chapters, w.SkinCSS); err != nil {
				return err
			}
			if _, err := tx.UpsertWork(ctx, w, model.FromPage, parse.Version); err != nil {
				return err
			}
		}
		if err := tx.UpsertSeries(ctx, model.Series{ID: SeriesPartial, Title: "Fixture Series Partial",
			Creators: []model.Creator{fixtureCreator}, Begun: "2025-01-02", Updated: "2025-06-07",
			SummaryHTML: "<p>Fixture series summary. " + lorem[0] + "</p>",
			NotesHTML:   "<p>Fixture series notes. " + lorem[1] + "</p>",
			Words:       9000, WorksCount: 4, Bookmarks: 3}); err != nil {
			return err
		}
		if err := tx.SetSeriesMembers(ctx, SeriesPartial, []store.SeriesMember{
			{WorkID: WorkChaptered, Position: 1}, {WorkID: seriesMissing, Position: 2}, {WorkID: WorkOneShot, Position: 3},
		}); err != nil {
			return err
		}
		if _, err := tx.QueueImages(ctx, WorkChaptered, []string{imagePNG, imageSVG, imageQueued}); err != nil {
			return err
		}
		queued, err := tx.NextImages(ctx, now.Add(time.Minute), 10, nil)
		if err != nil {
			return err
		}
		done := 0
		for _, row := range queued {
			img, ok := images[row.URL]
			if !ok {
				continue // imageQueued stays queued
			}
			if err := tx.ImageDone(ctx, row.ID, img.sha(), img.contentType, len(img.body)); err != nil {
				return err
			}
			done++
		}
		if done != len(images) {
			return fmt.Errorf("marked %d images downloaded, want %d", done, len(images))
		}
		return nil
	})
}

// storedImage is a downloaded image's bytes and stored content type.
type storedImage struct {
	body        []byte
	contentType string
}

func (i storedImage) sha() string {
	sum := sha256.Sum256(i.body)
	return hex.EncodeToString(sum[:])
}

// write puts the image where the image worker would have.
func (i storedImage) write(dir *os.Root) error {
	p := imagefetch.File(i.sha(), i.contentType)
	if err := dir.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return dir.WriteFile(p, i.body, 0o600)
}

// fixturePNG is a 32x32 two-colour square.
func fixturePNG() ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			c := color.RGBA{R: 0x2a, G: 0x6f, B: 0x97, A: 0xff}
			if (x/8+y/8)%2 == 0 {
				c = color.RGBA{R: 0xf4, G: 0xd3, B: 0x5e, A: 0xff}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// seedAccounts creates the test users (Accounts), each with its own TOTP secret and recovery
// codes, the way serve will read them: keys.db gets its first master key here, and serve
// reuses it.
func seedAccounts(ctx context.Context, dirs Dirs, log *slog.Logger, now time.Time) (accs map[string]Account, err error) {
	dir, err := rootdirs.PrivateSub(dirs.Data, dirs.Accounts)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	keys, err := store.OpenKeys(ctx, dir, store.Options{Logger: log})
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, keys.Close()) }()
	users, err := store.OpenUsers(ctx, dir, store.Options{Logger: log})
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, users.Close()) }()
	mk, err := controllers.NewMasterKeys(ctx, controllers.MasterKeysOptions{Keys: keys, Users: users, Logger: log,
		Now: func() time.Time { return now }})
	if err != nil {
		return nil, err
	}
	hasher, err := auth.NewHasher(mk.Keyring(), auth.HasherOptions{Params: cheapArgon2})
	if err != nil {
		return nil, err
	}
	accs = make(map[string]Account, len(Accounts))
	for i, key := range Accounts {
		// A fresh users.db numbers its users from 1, in creation order.
		acc, err := seedAccount(ctx, users, mk.Keyring(), hasher, int64(i)+1, Username(key), now)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		accs[key] = acc
	}
	return accs, nil
}

// seedAccount creates one test user, which must get userID.
func seedAccount(ctx context.Context, users *store.Users, secrets *auth.LiveKeyring, hasher *auth.Hasher, userID int64,
	username string, now time.Time) (Account, error) {
	if err := auth.CheckPassword(Password, username); err != nil {
		return Account{}, fmt.Errorf("fixture password: %w", err)
	}
	// Hash outside the transaction (txguard).
	phc, err := hasher.Hash(ctx, auth.UserPassword(userID), Password)
	if err != nil {
		return Account{}, err
	}
	totp := auth.NewTOTPSecret()
	codes := auth.NewRecoveryCodes()
	encs := make([][]byte, 0, len(codes))
	for _, c := range codes {
		n, ok := auth.NormalizeRecoveryCode(c)
		if !ok {
			return Account{}, errors.New("auth.NewRecoveryCodes made a code NormalizeRecoveryCode rejects")
		}
		encs = append(encs, secrets.Load().EncryptRecoveryCode(userID, n))
	}
	err = users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		id, err := tx.CreateUser(ctx, model.User{Username: username, PasswordPHC: phc,
			TOTPSecretEnc: secrets.Load().EncryptTOTPSecret(auth.UserTOTP(userID), totp),
			CreatedAt:     now, PasswordChangedAt: now})
		if err != nil {
			return err
		}
		if id != userID {
			return fmt.Errorf("created user %d, want %d (users.db not new?)", id, userID)
		}
		return tx.ReplaceRecoveryCodes(ctx, userID, encs)
	})
	if err != nil {
		return Account{}, err
	}
	return Account{Username: username, Password: Password, TOTPSecret: auth.EncodeTOTPSecret(totp),
		RecoveryCodes: codes}, nil
}

var fixtureCreator = model.Creator{User: "fixture_author", Pseud: "fixture_author"}

// lorem are the paragraphs every text is built from.
var lorem = []string{
	"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.",
	"Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.",
	"Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur.",
	"Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.",
}

// paragraphs is n paragraphs of lorem, as chapter HTML.
func paragraphs(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "<p>%s</p>\n", lorem[i%len(lorem)])
	}
	return b.String()
}

// richBody uses the markup AO3 chapters commonly carry, plus the three images.
func richBody() string {
	return "<p><em>Fixture emphasis.</em> <strong>Fixture strong.</strong> " + lorem[0] + "</p>\n" +
		`<p><img src="` + imagePNG + `" alt="fixture square"></p>` + "\n" +
		"<blockquote><p>" + lorem[1] + "</p></blockquote>\n" +
		`<p class="fixture-skinned">Fixture paragraph styled by the work skin.</p>` + "\n" +
		"<hr>\n<ul><li>Fixture item one</li><li>Fixture item two</li></ul>\n" +
		`<p><img src="` + imageSVG + `" alt="fixture shape"> <img src="` + imageQueued + `" alt="fixture queued"></p>` + "\n" +
		`<p><a href="https://example.test/fixture-link">Fixture external link</a></p>` + "\n" +
		paragraphs(6)
}

// hostileBody carries XSS payloads the sanitizer must neutralize (stage 7). A test can check
// that none of them ran by looking for window.__ao3mirrorXSS.
func hostileBody() string {
	return paragraphs(2) +
		`<script>window.__ao3mirrorXSS = "script";</script>` + "\n" +
		`<p><img src="x" onerror="window.__ao3mirrorXSS = 'onerror'"></p>` + "\n" +
		`<p><a href="javascript:window.__ao3mirrorXSS='href'">Fixture javascript link</a></p>` + "\n" +
		`<iframe src="https://example.test/fixture-frame"></iframe>` + "\n" +
		`<p style="position: fixed; inset: 0">Fixture overlay attempt</p>` + "\n" +
		paragraphs(2)
}

// works are the seeded works, as the parser would have produced them from their pages.
func works() []model.Work {
	tags := func(extra ...model.Tag) []model.Tag {
		return append([]model.Tag{
			{Type: model.TagRating, Name: "Fixture Rating"},
			{Type: model.TagWarning, Name: "Fixture Warning"},
			{Type: model.TagCategory, Name: "Fixture Category"},
			{Type: model.TagFandom, Name: "Fixture Fandom"},
		}, extra...)
	}
	return []model.Work{
		{
			ID: WorkChaptered, Title: "Fixture Work Chaptered", Creators: []model.Creator{fixtureCreator},
			SummaryHTML:  "<p>Fixture summary. " + lorem[0] + "</p>",
			NotesHTML:    "<p>Fixture work notes. " + lorem[1] + "</p>",
			EndNotesHTML: "<p>Fixture work end notes. " + lorem[2] + "</p>",
			Language:     "English", Words: 4200, ChaptersPosted: 3, ChaptersExpected: 4,
			Published: "2025-01-02", Revised: "2025-06-07",
			Stats: model.Stats{Kudos: 120, Hits: 3400, Comments: 15, Bookmarks: 20},
			Tags: tags(
				model.Tag{Type: model.TagRelation, Name: "Fixture Character A/Fixture Character B"},
				model.Tag{Type: model.TagCharacter, Name: "Fixture Character A"},
				model.Tag{Type: model.TagCharacter, Name: "Fixture Character B"},
				model.Tag{Type: model.TagFreeform, Name: "Fixture Freeform One"},
				model.Tag{Type: model.TagFreeform, Name: "Fixture Freeform Two"},
			),
			Series: []model.SeriesPart{{ID: SeriesPartial, Title: "Fixture Series Partial", Position: 1}},
			Chapters: []model.Chapter{
				{ID: 100101, Position: 1, Title: "Fixture Chapter One", Published: "2025-01-02",
					SummaryHTML: "<p>Fixture chapter summary.</p>", NotesHTML: "<p>Fixture chapter notes.</p>",
					BodyHTML: richBody()},
				{ID: 100102, Position: 2, Title: "Fixture Chapter Two", Published: "2025-03-04",
					BodyHTML: paragraphs(8), EndNotesHTML: "<p>Fixture chapter end notes.</p>"},
				{ID: 100103, Position: 3, Title: "", Published: "2025-06-07", BodyHTML: paragraphs(5)},
			},
			SkinCSS: "#workskin .fixture-skinned { color: #8a2be2; font-weight: bold; }\n" +
				"#workskin blockquote { border-left: 4px solid #2a6f97; }",
		},
		{
			ID: WorkOneShot, Title: "Fixture Work One-Shot", Creators: []model.Creator{fixtureCreator},
			SummaryHTML: "<p>Fixture one-shot summary.</p>", Language: "English", Words: 1500,
			ChaptersPosted: 1, ChaptersExpected: 1, Complete: true, Published: "2025-04-05", Revised: "2025-04-05",
			RevisedIsPublished: true, Stats: model.Stats{Kudos: 40, Hits: 900, Bookmarks: 4},
			Tags:     tags(model.Tag{Type: model.TagFreeform, Name: "Fixture Freeform One"}),
			Series:   []model.SeriesPart{{ID: SeriesPartial, Title: "Fixture Series Partial", Position: 3}},
			Chapters: []model.Chapter{{Position: 1, BodyHTML: paragraphs(10)}},
		},
		{
			ID: WorkAnonymous, Title: "Fixture Work Anonymous", Anonymous: true,
			SummaryHTML: "<p>Fixture anonymous summary.</p>", Language: "English", Words: 800,
			ChaptersPosted: 1, ChaptersExpected: 1, Complete: true, Published: "2025-05-06", Revised: "2025-05-06",
			RevisedIsPublished: true, Stats: model.Stats{Kudos: 7, Hits: 150},
			Tags:     tags(),
			Chapters: []model.Chapter{{Position: 1, BodyHTML: paragraphs(4)}},
		},
		{
			ID: WorkHostile, Title: "Fixture Work Hostile",
			Creators:    []model.Creator{fixtureCreator, {User: "fixture_coauthor", Pseud: "fixture_pseud"}},
			SummaryHTML: "<p>Fixture summary with a payload.</p><script>window.__ao3mirrorXSS = 'summary';</script>",
			NotesHTML:   `<p>Fixture notes.</p><img src="x" onerror="window.__ao3mirrorXSS = 'notes'">`,
			Language:    "English", Words: 2100, ChaptersPosted: 2, ChaptersExpected: 2, Complete: true,
			Published: "2025-02-03", Revised: "2025-02-10", Stats: model.Stats{Kudos: 12, Hits: 300, Comments: 2},
			Tags:        tags(model.Tag{Type: model.TagFreeform, Name: "Fixture Freeform Two"}),
			Collections: []model.Collection{{Name: "fixture_collection", Title: "Fixture Collection"}},
			Chapters: []model.Chapter{
				{ID: 100401, Position: 1, Title: "Fixture Chapter Calm", Published: "2025-02-03", BodyHTML: paragraphs(4)},
				{ID: 100402, Position: 2, Title: "Fixture Chapter Payloads", Published: "2025-02-10", BodyHTML: hostileBody()},
			},
			// The skin sanitizer must drop the import, the external url(), the font face and the
			// fixed overlay, and keep the colour.
			SkinCSS: "@import url(https://example.test/fixture.css);\n" +
				"@font-face { font-family: fixture; src: url(https://example.test/fixture.woff2); }\n" +
				"#workskin p { color: #b22222; background: url(https://example.test/fixture.png); }\n" +
				"#workskin .overlay { position: fixed; top: 0; left: 0; width: 100%; height: 100%; }",
		},
	}
}
