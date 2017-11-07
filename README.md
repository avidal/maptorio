Maptorio
========

`maptorio` is a command line tool to generate browseable web maps from a
factorio save file. The map library used is
[leaflet.js](http://leafletjs.com/).

This tool was inspired by the factorio mod [Google Maps Factorio style](https://mods.factorio.com/mods/credomane/FactorioMaps) by credomane.

Note that this is currently a major WORK IN PROGRESS. It has not been tested on
any version other than MacOS and currently does not support the Steam version
of the game. I'm looking for help testing.

[Here is an example map](https://heyviddy.com/~alex/megabase/), generated from
a megabase save uploaded by one of the devs that I found on
[reddit](https://reddit.com/r/factorio). Unfortunately, I no longer have a link
to the actual post.

The megabase linked above is...mega. The initial set of tiles (generated in-game)
take up 3.1 gigabytes across 6,786 images. The complete set of tiles takes up
3.6 gigabytes across 9,520 tiles.

A more representative example would be
[this map](https://heyviddy.com/~alex/extra-life-2017), which is 2,167 tiles
and 813 megabytes.

How it Works
------------

Maptorio works fairly simply:

- Create a temporary workspace, copying in the save file
- Generate a mod named `maptorio` by rendering a lua template script
- Start the game, pointing it to the temporary workspace
- The generated mod iterates over all chunks that have player built entities
  (or are next to a chunk with player built entities) and renders a screenshot
- Once the screenshots are generated, copy the screenshots to an output directory
- Kick off a process that iterates over the screenshots, building new tiles for
  higher zoom levels
- Copy in an index.html which loads leaflet.js (and a minimap) plugin

Requirements
------------

Currently, there are no pre-built binaries and so you'll need to clone this
repository and have a Go runtime available.

In addition, you'll need the standalone version of factorio (available from the
[factorio downloads](https://factorio.com/downloads) page). Note that the
factorio version you download *must* be greater than or equal to the factorio
version used to make the save you are rendering.

Setup and Usage
---------------

First, clone this repository and download the dependencies:

```
$ git clone git://github.com:avidal/maptorio.git
$ cd maptorio
$ go get ./...
```

Next, copy `maptorio.conf.example` to `maptorio.conf` and tweak to taste. The
most important setting is `binary-path`, which must point to the factorio
binary you downloaded.

Then, run it:

```
$ go run cmd/maptorio.go -c maptorio.conf <path to save file>
```

Once you start, assuming there are no errors, you'll see a Factorio game window
open. Do not close the window, it'll be closed automatically once all of the
screenshots are rendered.

After the screenshots are rendered and the game is closed, the screenshots will
be copied into a new directory, by default this will be "maptorio-<save name>"
in the current directory.

Maptorio will continue to run, but this time it will be rendering the extra
zoom levels by combining tiles from a previous zoom level.

Once it's complete, the output directory will contain:

- index.html (the actual main webpage)
- tiles/{4,10} (rendered map tiles)
- empty.jpg (a small black placeholder for empty tiles)

You should be able to open the index.html directly in your browser, or
alternatively you can upload the entire directory to a web server somewhere to
share it.

To Do
-----

Following is a list of things I still need to add support for (help is
appreciated!)

- [ ] Make the Steam version work
- [ ] Test on Windows
- [ ] Test on Linux
- [ ] Auto-detect factorio binary based on default install directories
- [ ] Cross-compile binaries and host them on Github
- [ ] Figure out the correct math for expected number of tiles. The progress
  bars generally run over.
- [ ] Update the mod to render chunks that are completely surrounded by other
  chunks that have been rendered (to avoid having an unrendered block in the
  middle of a factory)

In addition, *most* configuration options are not supported yet. You can view
maptorio.conf for a description of each.

- [ ] screenshot-resolution
- [ ] grow-chunks
- [ ] show-entity-info
- [ ] time-of-day
- [ ] mod-directory
- [ ] enabled-mods
