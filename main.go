package main

import (
    "bufio"
    "encoding/json"
    "flag"
    "fmt"
    "net"
    "net/http"
    "os"
    "os/signal"
    "path/filepath"
    "strings"
)

type Context struct {
    // Connection listener.
    ln net.Listener
    // Signals that the context should continue running
    running bool
}

func quitCleanup(c chan os.Signal, ctx *Context) {
    _ = <-c

    ctx.running = false
    if ctx.ln != nil {
        ctx.ln.Close()
    }
}

type SplitEntry struct {
    // Split's title/name/label.
    Label string `json:"label"`
    // Accumulated time from the start of the run.
    Time int `json:"time",omitempty`
}

type GameSplit struct {
    // Run with the best time ever
    Entries []SplitEntry `json:"entries"`
    // Theoretical if every split were the best ever
    Best []SplitEntry `json:"best",omitempty`
}

func saveData(path string, res *http.Response) {
}

func loadData(path string, res *http.Response, isJson bool) {
    var game GameSplit

    f, err := os.Open(path)
    if err != nil {
        // Shouldn't happen as it was previously checked
        res.StatusCode = http.StatusNotFound
        fmt.Printf("Failed to locate data '%s': %+v", path, err)
        return
    }

    // Check that the data is correct
    if isJson {
        dec := json.NewDecoder(f)
        err = dec.Decode(&game)
        if err != nil {
            res.StatusCode = http.StatusInternalServerError
            fmt.Printf("Failed load data '%s': %+v", path, err)
            f.Close()
            return
        }

        f.Seek(0, 0)

        res.Header = make(http.Header)
        res.Header.Add("Access-Control-Allow-Origin", "*")
        res.Header.Add("Access-Control-Allow-Methods", "POST, GET")
        res.Header.Add("Access-Control-Allow-Headers", "Content-Type, Access-Control-Allow-Origin")
        res.Header.Add("Access-Control-Max-Age", "86400")
        res.StatusCode = http.StatusOK
        res.Header.Add("Content-Type", "application/json")
    }

    // Return the data
    res.Body = f
    res.StatusCode = http.StatusOK
}

func setCORS(req *http.Request, res *http.Response) {
    res.Header = make(http.Header)
    res.Header.Add("Access-Control-Allow-Origin", "*")
    res.Header.Add("Access-Control-Allow-Methods", "POST, GET")
    res.Header.Add("Access-Control-Allow-Headers", "Content-Type, Access-Control-Allow-Origin")
    res.Header.Add("Access-Control-Max-Age", "86400")
    res.StatusCode = http.StatusOK
}

func Serve(conn net.Conn) {
    var res http.Response

    res.StatusCode = http.StatusForbidden
    res.Proto = "HTTP/1.0"
    res.ProtoMajor = 1
    res.ProtoMinor = 0

    defer conn.Close()
    defer func() {
        res.Status = http.StatusText(res.StatusCode)

        res.Write(conn)
    } ()

    req, err := http.ReadRequest(bufio.NewReader(conn))
    if err != nil {
        fmt.Printf("Failed to receive HTTP request: %+v", err)
        return
    }

    if !strings.HasPrefix(req.Proto, "HTTP/") {
        fmt.Printf("Received a non-HTTP request ('%s')\n", req.Proto)
        return
    } else if req.RequestURI == "" {
        fmt.Printf("Missing RequestURI in request\n")
        return
    }

    res.Proto = req.Proto
    res.ProtoMajor = req.ProtoMajor
    res.ProtoMinor = req.ProtoMinor

    isJson := (req.Header.Get("Content-Type") == "application/json")

    basePath := filepath.Clean(req.RequestURI)
    path := filepath.Join(".", basePath)
    path, err = filepath.Abs(path)
    if err != nil {
        fmt.Printf("Failed to get requested path '%s': %+v\n", basePath, err)
        return
    }
    if _, err = os.Stat(path); err != nil {
        res.StatusCode = http.StatusNotFound
        fmt.Printf("Requested data '%s' does not exist on the server: %+v\n", basePath, err)
        return
    }

    switch req.Method {
    case "POST":
        saveData(path, &res)
    case "GET":
        if isJson {
            path = filepath.Join(path, "best.json")
        }
        loadData(path, &res, isJson)
    case "OPTIONS":
        fmt.Printf("Header: %+v\n", req.Header)
        setCORS(req, &res)
    default:
        res.StatusCode = http.StatusMethodNotAllowed
        fmt.Printf("Invalid request method '%s'", req.Method)
    }
}

func main() {
    var url string
    var port int
    var ctx Context
    var err error

    flag.StringVar(&url, "url", "", "URL accepted by the server  (empty accepts any address/url).")
    flag.IntVar(&port, "port", 60000, "Port listening for splits requests.")
    flag.Parse()

    // Detect keyboard interrupts (Ctrl+C) and exit gracefully.
    signalTrap := make(chan os.Signal, 1)
    go quitCleanup(signalTrap, &ctx)
    signal.Notify(signalTrap, os.Interrupt)

    ctx.ln, err = net.Listen("tcp", fmt.Sprintf("%s:%d", url, port))
    if err != nil {
        panic(fmt.Sprintf("Failed to start listening on %s:%d: %+v", url, port , err))
    }
    defer ctx.ln.Close()

    ctx.running = true
    for ctx.running {
        conn, err := ctx.ln.Accept()
        if err != nil {
            fmt.Printf("Failed to accept connection: %+v\n", err)
            continue
        }

        go Serve(conn)
    }
}
