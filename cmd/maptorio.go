package main

import (
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/avidal/maptorio"
	"github.com/go-ini/ini"
	"github.com/spf13/pflag"
)

type iniconfig struct {
	ConfigPath string
	Binary     string `ini:"binary-path"`

	Resolution     int  `ini:"screenshot-resolution"`
	GrowChunks     int  `ini:"grow-chunks"`
	ShowEntityInfo bool `ini:"show-entity-info"`
	TimeOfDay      int  `ini:"time-of-day"`

	OutputDirectory    string `ini:"output-directory"`
	TemporaryDirectory string `ini:"temporary-directory"`

	initialized bool
}

func (c *iniconfig) String() string {
	return c.ConfigPath
}

func (c *iniconfig) Type() string {
	return "iniconfig"
}

func (c *iniconfig) Set(path string) error {
	var err error
	var cfg *ini.File
	c.initialized = true

	if c.ConfigPath, err = filepath.Abs(path); err != nil {
		return err
	}

	if cfg, err = ini.Load(c.ConfigPath); err != nil {
		return err
	}

	if err = cfg.MapTo(c); err != nil {
		return err
	}

	if stat, err := os.Stat(c.Binary); err != nil {
		return err
	} else if stat.IsDir() {
		return fmt.Errorf("invalid binary path '%s'", c.Binary)
	}

	if c.OutputDirectory, err = filepath.Abs(c.OutputDirectory); err != nil {
		return err
	}

	if c.Binary, err = filepath.Abs(c.Binary); err != nil {
		return err
	}

	// If they specify a temporary directory then make it into an absolute path
	if c.TemporaryDirectory != "" {
		if c.TemporaryDirectory, err = filepath.Abs(c.TemporaryDirectory); err != nil {
			return err
		}
	}

	if c.Resolution != 512 && c.Resolution != 1024 && c.Resolution != 2048 && c.Resolution != 4096 {
		return fmt.Errorf("invalid screenshot-resolution %d", c.Resolution)
	}

	if c.GrowChunks < 0 {
		return fmt.Errorf("invalid grow-chunks %d, must be greater than or equal to 0", c.GrowChunks)
	}

	if c.TimeOfDay < 0 || c.TimeOfDay > 23 {
		return fmt.Errorf("invalid time-of-day %d, must be between 0 and 23", c.TimeOfDay)
	}

	return nil

}

func main() {

	// Setup the config from ini as a global flag
	var config iniconfig
	var flags = pflag.NewFlagSet("", pflag.ExitOnError)
	flags.VarP(&config, "config", "c", "Config file to use")
	flags.Usage = func() {
		fmt.Println(`
USAGE: maptorio -c <config file> [command] [savefile]
`)
		flags.PrintDefaults()
	}

	// Ignoring the error here since we have pflag.ExitOnError set
	_ = flags.Parse(os.Args[1:])

	// Make sure they supplied a value for the config file
	if !config.initialized {
		fmt.Println("Error: no configuration file set")
		flags.Usage()
		os.Exit(2)
	}

	if flags.NArg() == 0 {
		fmt.Println("Error: missing save file or command")
		flags.Usage()
		os.Exit(2)
	}

	switch flags.Arg(0) {
	case "render":
		render(config, flags.Arg(1))
	case "mapgen":
		// If they explicitly want to generate the map, that requires setting the output directory
		// as the first argument
		config.OutputDirectory = flags.Arg(1)
		mapgen(config)
	default:
		// The default process is to first render the screenshots (which updates the config)
		// and then generate the map
		config = render(config, flags.Arg(0))
		mapgen(config)
	}
}

func render(config iniconfig, save string) iniconfig {
	var err error
	if save, err = filepath.Abs(save); err != nil {
		fmt.Printf("invalid save file %s; got: %s\n", save, err.Error())
		os.Exit(2)
	}

	if stat, err := os.Stat(save); err != nil {
		fmt.Printf("invalid save file %s; got: %s\n", save, err.Error())
		os.Exit(2)
	} else if stat.IsDir() {
		fmt.Printf("invalid save file %s; is a directory\n", save)
		os.Exit(2)
	} else if filepath.Ext(save) != ".zip" {
		fmt.Printf("invalid save file %s; is not a zip file\n", save)
		os.Exit(2)
	}

	config = prepareWorkspace(config, save)

	fmt.Printf("Rendering with save %s\n", save)

	// Start the game...
	var cmdargs = []string{
		"-v",
		"--disable-audio",
		fmt.Sprintf("--config=%s", filepath.Join(config.TemporaryDirectory, "config.ini")),
		fmt.Sprintf("--load-game=%s", filepath.Join(config.TemporaryDirectory, "save.zip")),
		fmt.Sprintf("--mod-dir=%s", filepath.Join(config.TemporaryDirectory, "mods")),
	}

	fmt.Println("Running factorio with args ", cmdargs)

	var cmd = exec.Command(config.Binary, cmdargs...)

	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Waiting for screenshots to render...")

	// After it starts we want to check for a rendered-tiles file in the script output. Once it writes we know
	// how many images to look for and once every one of them has rendered we can kill the process
	var sig = make(chan error, 1)
	go func() {
		// Before we start, wait for 15s for the game to even start
		<-time.After(15 * time.Second)

		var so = filepath.Join(config.TemporaryDirectory, "data", "script-output")
		var expected int
		for {
			var output []byte
			var err error
			if output, err = ioutil.ReadFile(filepath.Join(so, "rendered-tiles")); err != nil {
				fmt.Printf("Got error reading output file %s\n", err.Error())
				<-time.After(1 * time.Second)
				continue
			}

			fmt.Printf("Expecting to find %s tiles.\n", string(output))

			if expected, err = strconv.Atoi(string(output)); err != nil {
				sig <- err
			}

			// Break out of this loop since we know how many tiles we're supposed to have
			break
		}

		// Now we know how many we *should* have, so let's loop until we have that many files
		for {
			var files []string
			if files, err = filepath.Glob(filepath.Join(so, "tiles", "10") + "/*"); err != nil {
				sig <- err
			}

			if len(files) >= expected {
				fmt.Println("Found the expected number of tiles. Exiting game.")
				break
			}

			fmt.Printf("Found %d of %d tiles, waiting for more...\n", len(files), expected)
			<-time.After(1 * time.Second)
		}
		sig <- nil
	}()

	// The goroutine will send a signal over the sig channel when it has encountered various conditions, the error
	// will be nil if everything was successful. The primary issue is that if the game itself crashes for some reason
	// the worker will continue forever. So, we need to be aware of what it's doing and track its own lifecycle.
	// Namely, if the game quits before we receive a signal we can stop the process and report the problem, but otherwise
	// we want to wait until we receive a signal, then issue the stop.
	var pchan = make(chan error, 1)
	go func() {
		pchan <- cmd.Wait()
	}()

	select {
	case err = <-pchan:
		// NOTE: The user closing the game will cause the error to be nil. We still report this as an
		// abnormal exit because they shouldn't do that.
		log.Fatalf("Factorio exited abnormally. Got: %v\n", err)
	case err = <-sig:
		fmt.Printf("Got signal %v\n", err)
		cmd.Process.Kill()
	}

	// Now that the initial tile generation phase is complete, copy all of those tiles to the output directory
	// so the rest of the rendering can continue
	var sourceTiles = filepath.Join(config.TemporaryDirectory, "data", "script-output", "tiles")
	var destTiles = filepath.Join(config.OutputDirectory, "tiles")
	if err := copyDir(sourceTiles, destTiles); err != nil {
		log.Fatal(err)
	}

	return config
}

func mapgen(config iniconfig) {
	var od = config.OutputDirectory
	fmt.Printf("Making layers using output directory %s\n", od)

	// Before rendering, copy in the placeholder jpg which the renderer will
	// use as filler
	copyFile("empty.jpg", filepath.Join(od, "empty.jpg"))

	maptorio.Render(od)

	// After the rendering pass has completed, generate the index file
	copyFile("index.html", filepath.Join(od, "index.html"))

}

// prepareWorkspace makes the proper fs layout for running the game and rendering the screenshots
// it returns a modified iniconfig with the correct temporary and output directories
func prepareWorkspace(c iniconfig, save string) iniconfig {

	var td string
	var err error
	if td, err = ioutil.TempDir(c.TemporaryDirectory, "maptorio-"); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Using temporary directory %s\n", td)
	c.TemporaryDirectory = td

	// Create the output directory (where the rendered map itself will go)
	var savename = filepath.Base(save)
	savename = strings.TrimSuffix(savename, filepath.Ext(savename))
	var od = filepath.Join(c.OutputDirectory, fmt.Sprintf("maptorio-%s", savename))

	// Remove the current output directory if it exists
	if err := os.RemoveAll(od); err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(od, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	c.OutputDirectory = od

	// Create the directory for the mod itself
	if err = os.MkdirAll(filepath.Join(td, "mods", "maptorio_0.0.0"), os.ModePerm); err != nil {
		log.Fatal(err)
	}

	var configTemplate = `
; version=2
[path]
write-data={{.DataDir}}

[other]
check-updates=false
show-tips-and-tricks=false
show-tutorial-notifications=false
autosave-slots=0

[sound]
master-volume=0

[graphics]
;screenshots-queue-size=16
screenshots-threads-count=4
video-memory-usage=all
skip-vram-detection=true

; show-smoke=false
; show-decoratives=false
; show-clouds=false
`

	// Open a file to write the configuration to
	config, err := os.Create(filepath.Join(td, "config.ini"))
	if err != nil {
		log.Fatal(err)
	}

	var tmpl = template.Must(template.New("config.ini").Parse(configTemplate))
	if err = tmpl.Execute(config, struct{ DataDir string }{filepath.Join(td, "data")}); err != nil {
		log.Fatal(err)
	}

	var modInfo = `
{
    "name": "maptorio",
    "version": "0.0.0",
    "title": "Maptorio!",
    "author": "avidal",
    "contact": "alex.vidal@gmail.com",
    "homepage": "https://github.com/avidal/maptorio",
    "description": "Generates map tiles from screenshots to use with leaflet.js",
    "license": "MIT",
    "factorio_version": "0.15"
}
`
	if err := ioutil.WriteFile(filepath.Join(td, "mods", "maptorio_0.0.0", "info.json"), []byte(modInfo), os.ModePerm); err != nil {
		log.Fatal(err)
	}

	var modControl = `
-- maptorio-control.lua

local ticks = 0 
script.on_init(function()
    script.on_event(defines.events.on_tick, function()
        ticks = ticks + 1
        -- wait one tick before starting to avoid crashing!
        if ticks == 1 then
            generate()
        end
    end)
end)

function generate()
    -- When the game is initialized, take screenshots
    -- The screenshots are taken per-chunk at 1024x1024 at zoom 1, which means each screenshot will cover exactly 32x32 game tiles.

    local player = game.players[1]
    local surface = player.surface
    surface.always_day = true
    local force = player.force

    -- First, determine the boundaries of the entire map
    local topleft = { x=0, y=0 }
    local bottomright = { x=0, y=0 }

    local total_chunks = 0
    for chunk in surface.get_chunks() do
        if surface.is_chunk_generated(chunk) then
            topleft.x = math.min(topleft.x, chunk.x)
            topleft.y = math.min(topleft.y, chunk.y)
            bottomright.x = math.max(bottomright.x, chunk.x)
            bottomright.y = math.max(bottomright.y, chunk.y)
            total_chunks = total_chunks + 1
        end
    end

    local log = {}
    table.insert(log, "chunks=" .. total_chunks .. "; topleft=" .. topleft.x .. "x" .. topleft.y .. "; bottomright=" .. bottomright.x .. "x" .. bottomright.y)

    -- Now, topleft and bottomright contain *chunk* positions, not actual *positions*, which are game tiles
    -- This means that we will need to multiply chunk coordinates by 32 to get the origin position
    -- But because chunk coordinates are the center-most tile, subtract 16 to get the true topleft
    -- And we can iterate from top to bottom with one chunk of padding to make sure we get all of them.

    -- this counter catches the actual number of chunks that will be rendered, as opposed to total_chunks, which is the
    -- complete number of generated chunks
    local i = 0
    for x = topleft.x-1, bottomright.x+1, 1 do
        for y = topleft.y-1, bottomright.y+1, 1 do
            local items = 0
            local generated = surface.is_chunk_generated({x, y})
            
            if not generated then
                table.insert(log, "--> not generated, skipping.")
            else 
                -- if this chunk has a nearby chunk with any items in it, we'll render it
                local check_area = {
                    top_left = { x = (x - 1) * 32 - 16, y = (y - 1) * 32 - 16},
                    bottom_right= { x = (x + 1) * 32 + 16, y = (y + 1) * 32 + 16},
                }
                items = surface.count_entities_filtered({
                    area=check_area,
                    force="player",
                    limit=1 -- no need to keep searching; we either find something or we don't
                })
            end

            table.insert(log, "chunk=" .. x .. "x" .. y .. "y" .. "; has " .. items .. " items.")

            local position = { x = x * 32 - 16, y = y * 32 - 16 }
            table.insert(log, "--> position=" .. position.x .. "x" .. position.y)

            if items == 0 then
                table.insert(log, "--> no items, skipping.")
            end

            if items > 0 and generated then
                i = i + 1

                game.take_screenshot({
                    show_entity_info=true,
                    position=position,
                    resolution={1024,1024},
                    zoom=1,
                    path="tiles/10/" .. x .. "x" .. y .. ".jpg"
                })
            end
        end
    end

    game.write_file("log", table.concat(log, "\n"))
    game.write_file("rendered-tiles", i)
end
`
	if err := ioutil.WriteFile(filepath.Join(td, "mods", "maptorio_0.0.0", "control.lua"), []byte(modControl), os.ModePerm); err != nil {
		log.Fatal(err)
	}

	// Copy in the save file
	if err := copyFile(save, filepath.Join(td, "save.zip")); err != nil {
		log.Fatal(err)
	}

	return c
}

// copyFile copies the contents of the file named src to the file named
// by dst. The file will be created if it does not already exist. If the
// destination file exists, all it's contents will be replaced by the contents
// of the source file. The file mode will be copied from the source and
// the copied data is synced/flushed to stable storage.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer func() {
		if e := out.Close(); e != nil {
			err = e
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		return
	}

	err = out.Sync()
	if err != nil {
		return
	}

	si, err := os.Stat(src)
	if err != nil {
		return
	}
	err = os.Chmod(dst, si.Mode())
	if err != nil {
		return
	}

	return
}

// copyDir recursively copies a directory tree, attempting to preserve permissions.
// Source directory must exist, destination directory may exist.
// If destination exists, any existing files are overwritten.
// Symlinks are ignored and skipped.
func copyDir(src string, dst string) (err error) {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)

	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !si.IsDir() {
		return fmt.Errorf("source is not a directory")
	}

	_, err = os.Stat(dst)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = os.MkdirAll(dst, si.Mode())
	if err != nil {
		return err
	}

	entries, err := ioutil.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			err = copyDir(srcPath, dstPath)
			if err != nil {
				return err
			}
		} else {
			// Skip symlinks.
			if entry.Mode()&os.ModeSymlink != 0 {
				continue
			}

			err = copyFile(srcPath, dstPath)
			if err != nil {
				return err
			}
		}
	}

	return
}
