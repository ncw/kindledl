# Kindle Books Downloader

This is a program to download your purchased Kindle books from Amazon.

## Usage

First download the latest kindledl binary from the [releases page](https://github.com/ncw/kindledl/releases/latest).

You will need to run like this first. This will open a browser window which you should use to login to Amazon - then close the browser window. You may have to do this again if the integration stops working.

    kindledl -login

Once you have done this you can run this to start downloading books for the kindle device named.

    kindledl -kindle "Name of your Kindle"

If you are not running it on `amazon.co.uk` you may need to adjust some of the parameters (see below).

By default the books are stored in the current directory in a directory called "Books".

The files stored here will likely have DRM - this program does not remove the DRM. You can use USB to transfer these books to the kindle you named with the `-kindle` flag.

This takes about 35s per book to download. This is deliberately slow so as not to annoy Amazon. You can try to speed it up using the command line flags but don't be suprised if Amazon start taking countermeasures.

## Configuring for different country Amazons

### UK

The program comes set up for `amazon.co.uk`

### Others

You will likely have to change `-books-url` at minimum though other changes may be needed.

Edits to this README showing what parameters to use for different countries would be gratefully accepted (click the pencil icon above to get started).

## Command line help

Running `kindledl -h` will show this

```
Usage of ./kindledl:
  -book int
    	Book to start downloading from
  -books-per-page int
    	Books shown on each page (default 25)
  -books-url string
    	URL to show kindle books in date ordered, oldest first (default "https://www.amazon.co.uk/hz/mycd/digital-console/contentlist/booksPurchases/dateAsc/")
  -checkpoint string
    	File noting where the download has got to, ignored if -book is set (default "kindledl-checkpoint.txt")
  -debug
    	set to see debug messages
  -json
    	log in JSON format
  -kindle string
    	Name of the kindle to download for
  -login
    	set to launch login browser
  -msg-clear-furthest string
    	Text to look for in more actions menu to check it is OK (default "Clear Furthest Page Read")
  -msg-download-button string
    	Text to look for to find the download button (default "Download")
  -msg-download-usb string
    	Text to look for in more actions menu (default "Download & transfer via USB")
  -msg-more-actions string
    	Text to look for to find the more actions button (default "More actions")
  -msg-success string
    	Text to look for in the title of the success popup (default "Success")
  -output string
    	directory to store the downloaded books (default "Books")
  -rod string
    	Set the default value of options used by rod.
  -show
    	set to show the browser (not headless)
  -time-action-interval duration
    	Minimum time between browser actions (default 1s)
  -time-retry-sleep duration
    	Time to wait between retry of finding something on the page (default 1s)
  -time-scroll-pause duration
    	Time to wait after scrolling the page (default 500ms)
```

## Troubleshooting

If you want to see what the program is doing run it with the `-show` flag and it will open the browser that it is using and you can see exactly what is happening.

Running the `kindledl` command with the `-debug` flag shows more info about what it is doing.

    kindledl -debug -show

You can't run more than one instance kindledl at once. If you get the error 

    browser launch: [launcher] Failed to get the debug url: Opening in existing browser session.

Then there is another `kindledl` running or there is an orphan browser process you will have to kill.

## Limitations

- Currently only fetches one book at once.
- Currently the browser only has one profile so this can only be used with one amazon user. This is easy to fix.

## License

This is free software under the terms of the MIT license (check the LICENSE file included in this package).

## Contact and support

The project website is at:

- https://github.com/ncw/kindledl

There you can file bug reports, ask for help or contribute patches.
