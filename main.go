package main

import (
    "bufio"
    "encoding/json"
    "flag"
    "fmt"
    "io/ioutil"
    "net"
    "net/http"
    "os"
    "os/signal"
    "path/filepath"
    "strings"
    "time"
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

func saveData(newPath, bestPath string, req *http.Request, res *http.Response) {
    var newSplit GameSplit
    var bestSplit GameSplit

    // Open the best time, for comparison
    bestFp, err := os.OpenFile(bestPath, os.O_RDWR, 0644)
    if err != nil {
        res.StatusCode = http.StatusInternalServerError
        fmt.Printf("Failed to open split '%s': %+v", bestSplit, err)
        return
    }
    defer bestFp.Close()

    data, err := ioutil.ReadAll(bestFp)
    if err == nil {
        err = json.Unmarshal(data, &bestSplit)
    }
    if err != nil {
        res.StatusCode = http.StatusInternalServerError
        fmt.Printf("Failed to load split '%s': %+v", bestPath, err)
        fmt.Printf("Data: %+s", string(data))
        return
    }

    // Retrieve the latest time
    data, err = ioutil.ReadAll(req.Body)
    if err != nil {
        // TODO Failed to read body
        res.StatusCode = http.StatusInternalServerError
        fmt.Printf("Failed to get new data for '%s'", newPath, err)
        return
    }

    err = json.Unmarshal(data, &newSplit)
    if err != nil {
        // TODO Bad data
        res.StatusCode = http.StatusInternalServerError
        fmt.Printf("Failed to load split '%s': %+v", bestPath, err)
        fmt.Printf("Data: %+s", string(data))
        return
    }

    err = ioutil.WriteFile(newPath, data, 0644)
    if err != nil {
        res.StatusCode = http.StatusInternalServerError
        fmt.Printf("Failed to store data '%s': %+v", newPath, err)
        return
    }

    if len(bestSplit.Entries) == 0 || len(bestSplit.Entries) != len(newSplit.Entries) {
        res.StatusCode = http.StatusMultiStatus
        fmt.Printf("Failed to store data '%s': %+v", bestPath, err)
        return
    }

    // TODO Store best per-split time

    if idx := len(bestSplit.Entries) - 1; newSplit.Entries[idx].Time < bestSplit.Entries[idx].Time ||
            bestSplit.Entries[idx].Time == 0 {
        l := copy(bestSplit.Entries, newSplit.Entries)
        if l != len(bestSplit.Entries) {
            res.StatusCode = http.StatusMultiStatus
            fmt.Printf("Failed to update local data: %+v", err)
            return
        }

        data, err = json.Marshal(&bestSplit)
        if err != nil {
            res.StatusCode = http.StatusMultiStatus
            fmt.Printf("Failed to format updated data: %+v", err)
            return
        }

        // TODO If we succeded when writing to the file but fail to truncate its
        // length, the JSON object may become corrupt!! Fix this!
        bestFp.Seek(0, 0)
        _, err = bestFp.Write(data)
        if err != nil {
            res.StatusCode = http.StatusMultiStatus
            fmt.Printf("Failed to store updated data '%s': %+v", newPath, err)
            return
        }
        err = bestFp.Truncate(int64(len(data)))
        if err != nil {
            res.StatusCode = http.StatusMultiStatus
            fmt.Printf("Failed fix updated data '%s': %+v", newPath, err)
            fmt.Printf("WARNING: The file may have gotten corrupted!")
            return
        }
    }

    res.StatusCode = http.StatusAccepted
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

    if req.Body != nil {
        defer req.Body.Close()
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
        if isJson {
            now := time.Now()
            newPath := now.Format("2006-01-02T15:04:05") + ".json"
            newPath = filepath.Join(path, newPath)
            bestPath := filepath.Join(path, "best.json")
            saveData(newPath, bestPath, req, &res)
        } else {
            res.StatusCode = http.StatusMethodNotAllowed
            fmt.Printf("Cannot POST non-JSON data")
            return
        }
    case "GET":
        if isJson {
            path = filepath.Join(path, "best.json")
            loadData(path, &res, isJson)
        } else {
            res.StatusCode = http.StatusMethodNotAllowed
            fmt.Printf("Cannot GET non-JSON data")
            return
        }
    case "OPTIONS":
        // Allowed, but has empty response
    default:
        res.StatusCode = http.StatusMethodNotAllowed
        fmt.Printf("Invalid request method '%s'", req.Method)
        return
    }

    setCORS(req, &res)
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
    defer func() {
        ctx.ln.Close()
        ctx.ln = nil
    } ()

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
