package uci

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func loadExpected(t *testing.T, name string) *config {
	t.Helper()

	f, err := os.Open(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("cannot open %s.json: %v", name, err)
	}
	defer f.Close()

	expected := &config{}
	err = json.NewDecoder(f).Decode(&expected)
	if err != nil {
		t.Fatalf("error decoding json: %v", err)
	}

	// The JSON dump does not contain empty slices (they're marked with
	// "omitempty"), but the decoder creates them anyway. To get the tests
	// to pass, we need to eliminate nil slices (sections of config and
	// options of section) manually.
	if expected.Sections == nil {
		expected.Sections = []*section{}
	}
	for _, sec := range expected.Sections {
		if sec.Options == nil {
			sec.Options = []*option{}
		}
	}
	return expected
}

func TestLoadConfig(t *testing.T) {
	tt := []string{"system", "emptyfile", "emptysection", "luci", "ucitrack"}
	for i := range tt {
		name := tt[i]
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			r := NewTree("testdata")
			err := r.LoadConfig(name, false)
			assert.NoError(err)

			actual := r.(*tree).configs[name]

			if dump["json"] {
				assert.NoError(json.NewEncoder(os.Stderr).Encode(actual))
			}

			expected := loadExpected(t, name)
			assert.EqualValues(expected, actual)
		})
	}
}

func TestLoadConfig_nonExistent(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")
	err := r.LoadConfig("nonexistent", false)

	// os.IsNotExist fails on 1.16, https://golang.org/issue/44349
	assert.True(errors.Is(err, os.ErrNotExist))
}

func TestLoadConfig_forceReload(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")

	err := r.LoadConfig("system", false)
	assert.NoError(err)

	err = r.LoadConfig("system", false)
	assert.True(IsConfigAlreadyLoaded(err))

	err = r.LoadConfig("system", true)
	assert.NoError(err)
}

func TestLoadConfig_invalidFile(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")

	err := r.LoadConfig("invalid", false)
	assert.True(IsParseError(err))
}

func TestWriteConfig(t *testing.T) {
	tt := []string{"system", "emptyfile", "emptysection", "luci", "ucitrack"}
	for i := range tt {
		name := tt[i]
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			r := NewTree("testdata")
			err := r.LoadConfig(name, false)
			assert.NoError(err)

			actual := r.(*tree).configs[name]
			var buf bytes.Buffer
			_, err = actual.WriteTo(&buf)
			assert.NoError(err)

			if dump["serialized"] {
				fmt.Fprint(os.Stderr, buf.String())
			}

			// TODO: validate content of buf
		})
	}
}

func TestGetSections(t *testing.T) {
	assert := assert.New(t)

	r := NewTree("testdata")

	names, exists := r.GetSections("system", "system")
	assert.True(exists)
	assert.ElementsMatch(names, []string{"@system[0]"})

	names, exists = r.GetSections("system", "timeserver")
	assert.True(exists)
	assert.ElementsMatch(names, []string{"ntp"})

	names, exists = r.GetSections("nonexistent", "foo")
	assert.False(exists)
	assert.Nil(names)

	names, exists = r.GetSections("anonymous", "anon1")
	assert.True(exists)
	assert.ElementsMatch(names, []string{"@anon1[0]"})

	names, exists = r.GetSections("anonymous", "anon2")
	assert.True(exists)
	assert.ElementsMatch(names, []string{"@anon2[0]", "@anon2[1]"})

	names, exists = r.GetSections("anonymous", "anon3")
	assert.True(exists)
	assert.ElementsMatch(names, []string{"@anon3[0]", "@anon3[1]", "@anon3[2]"})
}

func TestAddSection(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")

	assert.NoError(r.AddSection("nonexistent", "a", "section"))

	assert.NoError(r.AddSection("system", "foo", "foo"))
	assert.True(r.Set("system", "foo", "bar", "42"))
	values, exists := r.Get("system", "foo", "bar")
	assert.True(exists)
	assert.ElementsMatch(values, []string{"42"})

	assert.Error(r.AddSection("system", "foo", "notfoo"))
	assert.NoError(r.AddSection("system", "foo", "foo"))

	assert.NoError(r.AddSection("nonexistent", "a", "section"))
	assert.True(r.Set("nonexistent", "a", "section", "value"))
	values, exists = r.Get("nonexistent", "a", "section")
	assert.True(exists)
	assert.ElementsMatch(values, []string{"value"})
}

func TestDelSection(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")

	names, exists := r.GetSections("system", "timeserver")
	assert.True(exists)
	assert.ElementsMatch(names, []string{"ntp"})
	r.DelSection("system", "ntp")

	names, exists = r.GetSections("system", "timeserver")
	assert.True(exists)
	assert.Len(names, 0)

	_, exists = r.GetSections("nonexistent", "foo")
	assert.False(exists)
	r.DelSection("nonexistent", "@foo[0]")

	_, exists = r.GetSections("nonexistent", "foo")
	assert.False(exists)
}

// TestDelSection_anonymous regression-tests deleting an anonymous section by
// its GetSections-returned synthetic "@type[index]" selector.
// config.Del previously matched only by a section's literal Name field,
// which is empty for an anonymous section — so DelSection("@system[0]")
// silently matched nothing and removed nothing, while the caller (and a
// following Commit()) saw no error at all.
func TestDelSection_anonymous(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")

	// testdata/system's own `config system` stanza has no explicit name, so
	// GetSections hands back its positional selector.
	names, exists := r.GetSections("system", "system")
	assert.True(exists)
	assert.ElementsMatch(names, []string{"@system[0]"})

	r.DelSection("system", "@system[0]")

	names, exists = r.GetSections("system", "system")
	assert.True(exists)
	assert.Len(names, 0)

	// A sibling named section in the same file is untouched.
	names, exists = r.GetSections("system", "timeserver")
	assert.True(exists)
	assert.ElementsMatch(names, []string{"ntp"})
}

func TestGet(t *testing.T) {
	assert := assert.New(t)

	r := NewTree("testdata")

	values, exists := r.Get("system", "ntp", "server")
	assert.True(exists)
	assert.ElementsMatch(values, []string{
		"0.lede.pool.ntp.org",
		"1.lede.pool.ntp.org",
		"2.lede.pool.ntp.org",
		"3.lede.pool.ntp.org",
	})

	values, exists = r.Get("system", "@system[0]", "timezone")
	assert.True(exists)
	assert.ElementsMatch(values, []string{"UTC"})

	values, exists = r.Get("system", "nonexistent", "foo")
	assert.False(exists)
	assert.Nil(values)
}

func TestDel(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")

	values, _ := r.Get("system", "ntp", "enabled")
	assert.ElementsMatch(values, []string{"1"})
	r.Del("system", "ntp", "enabled")
	values, _ = r.Get("system", "ntp", "enabled")
	assert.ElementsMatch(values, []string{})

	_, exists := r.Get("system", "nonexistent", "foo")
	assert.False(exists)
	r.Del("system", "nonexistent", "foo")
	_, exists = r.Get("system", "nonexistent", "foo")
	assert.False(exists)

	_, exists = r.Get("nonexistent", "foo", "bar")
	assert.False(exists)
	r.Del("nonexistent", "foo", "bar")
	_, exists = r.Get("nonexistent", "foo", "bar")
	assert.False(exists)

	// without prior loading
	r.Del("nonexistent2", "foo2", "bar2")
	_, exists = r.Get("nonexistent2", "foo2", "bar2")
	assert.False(exists)
}

func TestSet(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")

	assert.True(r.Set("system", "ntp", "enabled", "0"))
	values, exists := r.Get("system", "ntp", "enabled")
	assert.True(exists)
	assert.ElementsMatch(values, []string{"0"})

	values, exists = r.Get("system", "@system[0]", "hostname")
	assert.True(exists)
	assert.ElementsMatch(values, []string{"testhost"})

	assert.True(r.Set("system", "@system[0]", "hosttest"))

	assert.False(r.Set("system", "nonexistent", "foo", "bar"))
	values, exists = r.Get("system", "nonexistent", "foo")
	assert.False(exists)
	assert.Nil(values)

	assert.False(r.Set("nonexistent", "foo", "bar", "42"))
	values, exists = r.Get("nonexistent", "foo", "bar")
	assert.False(exists)
	assert.Nil(values)
}

func TestListDelete(t *testing.T) {
	assert := assert.New(t)

	r := NewTree("testdata")

	val, _ := r.Get("system", "ntp", "server")
	assert.NotEmpty(val)

	r.Del("system", "ntp", "server")

	val, _ = r.Get("system", "ntp", "server")
	assert.Empty(val)
}

func TestGetLast_Success(t *testing.T) {
	assert := assert.New(t)

	r := NewTree("testdata")

	val, ok := r.GetLast("system", "ntp", "server")
	assert.True(ok)

	assert.Equal(val, "3.lede.pool.ntp.org")
}

func TestGetLast_Failure(t *testing.T) {
	assert := assert.New(t)

	r := NewTree("testdata")

	_, ok := r.GetLast("system", "ntp", "port")
	assert.False(ok)
}

func TestGetBool_False(t *testing.T) {
	assert := assert.New(t)

	r := NewTree("testdata")

	val, ok := r.GetBool("wireless", "guest_radio0", "disabled")
	assert.True(ok)

	assert.False(val)
}

func TestGetBool_True(t *testing.T) {
	assert := assert.New(t)

	r := NewTree("testdata")

	val, ok := r.GetBool("wireless", "guest_radio1", "disabled")
	assert.True(ok)

	assert.True(val)
}

func TestGetBool_Other(t *testing.T) {
	assert := assert.New(t)

	r := NewTree("testdata")

	_, ok := r.GetBool("wireless", "guest_radio0", "mode")
	assert.False(ok)
}

func TestRevert(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")
	tree := r.(*tree)

	assert.NoError(r.LoadConfig("system", false))
	assert.Len(tree.configs, 1)

	// revert all
	r.Revert()
	assert.Len(tree.configs, 0)

	assert.NoError(r.LoadConfig("system", false))
	assert.Len(tree.configs, 1)

	// taint tree
	assert.True(r.Set("system", "ntp", "foo", "42"))
	assert.True(tree.configs["system"].tainted)
	r.Revert("system")
	assert.Len(tree.configs, 0)
}

// TestAddSection_afterRevertAll regression-tests adding a section to a config
// that does not exist on disk after a no-argument Revert().
//
// Revert() with no arguments used to set tree.configs to nil outright, rather
// than to an empty map. AddSection's "config is not on disk, synthesize it"
// branch then assigned straight into that nil map, panicking with
// "assignment to entry in nil map" — even though loadConfig had always
// guarded the very same assignment with a nil check.
func TestAddSection_afterRevertAll(t *testing.T) {
	assert := assert.New(t)
	r := NewTree("testdata")

	// Stage some state, then discard all of it. This is the documented way
	// to drop every staged change without touching the file system.
	assert.NoError(r.LoadConfig("system", false))
	r.Revert()

	// A config that genuinely does not exist under testdata/, so AddSection
	// has to synthesize it rather than load it.
	assert.NotPanics(func() {
		assert.NoError(r.AddSection("nonexistent", "a", "section"))
	})

	// The synthesized config is fully usable afterwards.
	assert.True(r.Set("nonexistent", "a", "option", "42"))
	values, exists := r.Get("nonexistent", "a", "option")
	assert.True(exists)
	assert.ElementsMatch(values, []string{"42"})

	// Reverting everything a second time leaves the tree in the same
	// reusable state, rather than a one-shot nil.
	r.Revert()
	assert.NotPanics(func() {
		assert.NoError(r.AddSection("nonexistent", "b", "section"))
	})
}

// TestGetSections_concurrent regression-tests GetSections against concurrent
// writers.
//
// GetSections was the only method touching tree.configs without holding the
// tree's mutex, yet it reaches loadConfig (via ensureConfigLoaded), which
// *writes* tree.configs — loadConfig's own doc comment requires the caller to
// hold the lock. Racing it against any writer is an unsynchronised map
// read/write, which the Go runtime turns into a fatal "concurrent map read and
// map write" rather than merely a wrong result. The tree is process-wide and
// shared, so this is the normal access pattern, not a corner case.
//
// Run under -race to observe the failure on unfixed code.
func TestGetSections_concurrent(t *testing.T) {
	const goroutines = 4
	const iterations = 50

	r := NewTree("testdata")
	var wg sync.WaitGroup

	start := make(chan struct{})
	spawn := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				fn()
			}
		}()
	}

	for i := 0; i < goroutines; i++ {
		// Readers. "system" always exists under testdata/, so this must
		// always report the config as present.
		spawn(func() {
			if _, exists := r.GetSections("system", "system"); !exists {
				t.Error("GetSections: expected config system to exist")
			}
		})

		// Writers: each of these mutates tree.configs or a config within it.
		spawn(func() { _ = r.LoadConfig("system", true) })
		spawn(func() { _ = r.AddSection("nonexistent", "a", "section") })
		spawn(func() { r.Set("nonexistent", "a", "option", "42") })
	}

	close(start)
	wg.Wait()
}

func TestCommit(t *testing.T) {
	origNewTmpFile := newTmpFile
	m := &mockTempFile{}
	newTmpFile = func(_, _ string) (tmpFile, error) { return m, nil }
	defer func() { newTmpFile = origNewTmpFile }()

	assert := assert.New(t)
	r := NewTree("testdata")

	// untainted save
	assert.NoError(r.Commit())

	// taint the tree
	assert.NoError(r.AddSection("cfgname", "secname", "sectype"))
	assert.True(r.Set("cfgname", "secname", "optname", "optvalue"))
	const content = "\nconfig sectype 'secname'\n\toption optname 'optvalue'\n\n"

	// try saving, but let it fail at different points
	reset := func(onwrite, onchmod, onsync, onrename error) {
		m.Buffer.Reset()
		m.ExpectedCalls = nil
		m.On("Close").Return(nil)
		m.On("Remove").Return(nil)
		m.On("Write", mock.AnythingOfType("[]uint8")).Return(onwrite)
		m.On("Chmod", os.FileMode(0644)).Return(onchmod)
		m.On("Sync").Return(onsync)
		m.On("Rename", "testdata/cfgname").Return(onrename)
	}

	reset(errors.New("fail write"), nil, nil, nil) //nolint:goerr113
	assert.EqualError(r.Commit(), "fail write")
	assert.Equal(0, m.Buffer.Len())

	reset(nil, errors.New("fail chmod"), nil, nil) //nolint:goerr113
	assert.EqualError(r.Commit(), "save: failed to set permissions: fail chmod")
	assert.EqualValues(content, m.Buffer.String())

	reset(nil, nil, errors.New("fail sync"), nil) //nolint:goerr113
	assert.EqualError(r.Commit(), "save: failed to sync: fail sync")

	reset(nil, nil, nil, errors.New("fail rename")) //nolint:goerr113
	assert.EqualError(r.Commit(), "save: failed to replace existing config: fail rename")

	reset(nil, nil, nil, nil)
	assert.NoError(r.Commit())
}

type mockTempFile struct {
	mock.Mock
	bytes.Buffer
}

func (m *mockTempFile) Write(p []byte) (int, error) {
	args := m.Called(p)
	if err := args.Error(0); err != nil {
		return 0, err // nolint:wrapcheck
	}
	n, _ := m.Buffer.Write(p)
	return n, nil
}

func (m *mockTempFile) Chmod(mode os.FileMode) error {
	args := m.Called(mode)
	return args.Error(0)
}

func (m *mockTempFile) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockTempFile) Remove() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockTempFile) Rename(newpath string) error {
	args := m.Called(newpath)
	return args.Error(0)
}

func (m *mockTempFile) Sync() error {
	args := m.Called()
	return args.Error(0)
}
