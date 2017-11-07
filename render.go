package maptorio

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/cheggaaa/pb"
	"github.com/mdlayher/imagegrid"
	"github.com/nfnt/resize"
)

var empty image.Image

const maxZoom = 10

type point struct {
	x, y int
}

func (p *point) String() string {
	return fmt.Sprintf("(%d, %d)", p.x, p.y)
}

// Max of 48 operations running at a time
var limiter = make(chan struct{}, 48)

func Render(wd string) {
	// Read in the empty jpeg to use as filler
	var path string
	var emptyF *os.File
	var err error

	if path, err = filepath.Abs(filepath.Join(wd, "empty.jpg")); err != nil {
		panic(err)
	}

	fmt.Println("using empty asset path of", path)
	if emptyF, err = os.Open(path); err != nil {
		panic(err)
	}
	defer emptyF.Close()

	if empty, err = jpeg.Decode(emptyF); err != nil {
		panic(err)
	}

	for z := 9; z >= 0; z-- {
		if hasmore := makeLevel(wd, z); !hasmore {
			break
		}
	}
}

func makeLevel(wd string, z int) bool {
	fmt.Printf("Making zoom level %d.\n", z)

	var topleft, bottomright = determineArea(wd, z+1)

	// Determine the total number of tiles necessary for this layer so we can initialize the progress bar
	var width = bottomright.x - topleft.x
	var height = bottomright.y - topleft.y

	var total = int(math.Ceil(float64(width)/2) * math.Ceil(float64(height)/2))

	fmt.Printf("  topleft: %+v; bottomright: %+v; total: %d\n", topleft, bottomright, total)
	var bar = pb.StartNew(total)

	var wg sync.WaitGroup

	// Now that we know the topleft and bottomright, we can iterate over those and make a new layer
	// If the bottom right is an even number we want to generate a tile
	// But if it's odd, we don't. This is covered by incrementing by 2 and catching ourselves
	// once we go over
	// The lowest zoom level should render as a single tile containing the entire map plus black
	// borders to fill in any gaps
	for x := topleft.x; x <= bottomright.x; x += 2 {
		for y := topleft.y; y <= bottomright.y; y += 2 {
			// We can jump by 2 for each iteration because we're going to fold 2x2 tiles in each subsequent image
			fmt.Printf("Making tile for %d, %d to %d, %d\n", x, y, x+1, y+1)
			wg.Add(1)
			go func(z, x, y int) {
				// Block until there's an available token
				limiter <- struct{}{}

				defer func() {
					<-limiter
					bar.Increment()
					wg.Done()
				}()

				makeTile(wd, z, x, y)

			}(z, x, y)
		}
	}

	wg.Wait()
	bar.FinishPrint(fmt.Sprintf("Completed zoom level %d\n", z))

	// As soon as we hit the point where we're only generating 1 tile, stop processing
	if total <= 1 {
		return false
	}

	return true
}

func makeTile(wd string, z, x, y int) {
	// makes a single tile
	var tiles = readImages(wd, z+1, x, y)
	if tiles == nil {
		//fmt.Printf("Skipping tile (%d,%d)@%d because there are no source tiles\n", x, y, z)
		return
	}

	var tile, err = imagegrid.Draw(2, tiles)
	if err != nil {
		panic(err)
	}

	// Resize to half and write it back out
	var im = resize.Thumbnail(1024, 1024, tile, resize.Bicubic)

	// And write the resized image
	//writeImage(z, x/pow(2, (maxZoom-z)), y/pow(2, (maxZoom-z)), im)
	//fmt.Printf("Writing tile (%d,%d)@%d to tile %dx%d.jpg\n", x, y, z, half(x), half(y))
	writeImage(wd, z, half(x), half(y), im)
}

func readImages(wd string, z, x, y int) []image.Image {
	// this function takes in a source coordinate and returns the 2x2 square of source images
	// but if all of the source images are empty we can skip it
	var tl = readImage(wd, z, x, y)
	var tr = readImage(wd, z, x+1, y)
	var bl = readImage(wd, z, x, y+1)
	var br = readImage(wd, z, x+1, y+1)

	if tl == empty && tr == empty && bl == empty && br == empty {
		return nil
	}

	return []image.Image{tl, tr, bl, br}
}

func readImage(wd string, z, x, y int) image.Image {
	var fd *os.File
	var err error
	var path = filepath.Join(wd, "tiles", strconv.Itoa(z), fmt.Sprintf("%dx%d.jpg", x, y))
	if fd, err = os.Open(path); os.IsNotExist(err) {
		return empty
	} else if err != nil {
		panic(err)
	}
	defer fd.Close()

	var im image.Image
	if im, err = jpeg.Decode(fd); err != nil {
		panic(fmt.Sprintf("got error decoding jpeg %s: %s", fd.Name(), err))
	}

	return im
}

func writeImage(wd string, z, x, y int, im image.Image) error {
	// Make sure the directory exists first
	var d = filepath.Join(wd, "tiles", strconv.Itoa(z))
	os.Mkdir(d, os.ModePerm)
	var out, err = os.Create(filepath.Join(d, fmt.Sprintf("%dx%d.jpg", x, y)))
	if err != nil {
		panic(err)
	}
	defer out.Close()

	var buf = new(bytes.Buffer)

	if err = jpeg.Encode(buf, im, nil); err != nil {
		panic(fmt.Sprintf("error encoding file %s, got %s", out.Name(), err))
	}

	io.Copy(out, buf)

	return nil
}

func determineArea(wd string, z int) (point, point) {
	var d = filepath.Join(wd, "tiles", strconv.Itoa(z))
	var files, _ = filepath.Glob(filepath.Join(d, "*.jpg"))

	// First, we need to figure out the absolute topleft and bottomright of the source tiles
	// this is used to iterate over and build subsequent layers
	var topleft = point{0, 0}
	var bottomright = point{0, 0}
	fmt.Printf("Determining area for zoom level %d\n", z)

	for _, fp := range files {
		var f = filepath.Base(fp)
		f = strings.TrimSuffix(f, ".jpg")
		//fmt.Printf("\t%s : %s\n", fp, f)

		var parts = strings.Split(f, "x")
		var x, _ = strconv.Atoi(parts[0])
		var y, _ = strconv.Atoi(parts[1])

		topleft.x = min(topleft.x, x-abs(x%2))
		topleft.y = min(topleft.y, y-abs(y%2))
		bottomright.x = max(bottomright.x, x)
		bottomright.y = max(bottomright.y, y)
	}

	return topleft, bottomright
}

func abs(a int) int {
	return int(math.Abs(float64(a)))
}

func min(a, b int) int {
	if a < b {
		return a
	} else if a > b {
		return b
	}

	return a

}

func max(a, b int) int {
	if a < b {
		return b
	} else if a > b {
		return a
	}

	return b
}

func pow(base, exp int) int {
	var f = math.Pow(float64(base), float64(exp))
	return int(f)
}

func zoom(scale float64) int {
	return int(math.Log(scale) / math.Ln2)
}

func scale(zoom int) int {
	return pow(2, zoom)
}

func half(n int) int {
	return int(math.Floor(float64(n) / 2.0))
}
