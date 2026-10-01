package httpapi

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/btrvodka/redigate/internal/auth"
	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

// execParams are shared by /command and /pipeline. Each of them can also be
// passed as a query parameter, the body takes precedence.
type execParams struct {
	// Where the command goes: auto routes by key.
	Target string `json:"target" enums:"auto,node,masters,replicas,all,sentinels"`
	// Node address for target=node.
	Node string `json:"node"`
	// Database (standalone and sentinel).
	DB *int `json:"db"`
	// Encoding of binary strings in the reply.
	Encoding string `json:"encoding" enums:"auto,utf8,base64"`
}

type CommandRequest struct {
	execParams

	// Command and arguments: strings, numbers or {"base64": "..."}.
	Args codec.Args `json:"args"`
	// A redis-cli style command line, alternative to args.
	Command string `json:"command"`
}

// handleCommand runs a single command.
//
// JSON body: {"args": ["SET", "key", "value"]} or {"command": "SET key value"}.
// Any other body is treated as a redis-cli style command line: SET key "a value".
func (s *Server) handleCommand(r *http.Request) (any, error) {
	body, isJSON, err := readBody(r)
	if err != nil {
		return nil, err
	}

	var req CommandRequest

	if isJSON {
		if err := decodeJSON(body, &req); err != nil {
			return nil, err
		}

		if req.Command != "" {
			if len(req.Args) > 0 {
				return nil, badRequest("args and command are mutually exclusive")
			}

			if req.Args, err = codec.SplitArgs(req.Command); err != nil {
				return nil, badRequest("invalid command: %v", err)
			}
		}
	} else if req.Args, err = codec.SplitArgs(string(body)); err != nil {
		return nil, badRequest("invalid command: %v", err)
	}

	opts, err := execOptions(r, req.execParams)
	if err != nil {
		return nil, err
	}

	return s.svc.Exec(r.Context(), req.Args, opts)
}

type PipelineRequest struct {
	execParams

	// Commands: arrays of arguments or command lines.
	Commands []pipelineCommand `json:"commands"`
	// Wrap the commands into MULTI/EXEC.
	Atomic *bool `json:"atomic"`
	// Run on a dedicated connection: SELECT, AUTH, WATCH and MULTI are allowed.
	Session *bool `json:"session"`
}

// pipelineCommand is either an array of arguments or a command line.
type pipelineCommand codec.Args

func (c *pipelineCommand) UnmarshalJSON(data []byte) error {
	var line string
	if err := json.Unmarshal(data, &line); err == nil {
		args, err := codec.SplitArgs(line)
		if err != nil {
			return fmt.Errorf("invalid command %q: %w", line, err)
		}

		*c = pipelineCommand(args)

		return nil
	}

	return (*codec.Args)(c).UnmarshalJSON(data)
}

// handlePipeline runs several commands in one round trip.
//
// JSON body: {"commands": [["SET", "a", "1"], "GET a"], "atomic": false, "session": false}.
// Any other body is treated as one redis-cli style command per line, lines starting with # are skipped.
func (s *Server) handlePipeline(r *http.Request) (any, error) {
	body, isJSON, err := readBody(r)
	if err != nil {
		return nil, err
	}

	var (
		req      PipelineRequest
		commands []codec.Args
	)

	if isJSON {
		if err := decodeJSON(body, &req); err != nil {
			return nil, err
		}

		for _, cmd := range req.Commands {
			commands = append(commands, codec.Args(cmd))
		}
	} else if commands, err = splitLines(string(body)); err != nil {
		return nil, err
	}

	execOpts, err := execOptions(r, req.execParams)
	if err != nil {
		return nil, err
	}

	opts := service.PipelineOptions{ExecOptions: execOpts}

	if opts.Atomic, err = boolParam(r, req.Atomic, "atomic"); err != nil {
		return nil, err
	}

	if opts.Session, err = boolParam(r, req.Session, "session"); err != nil {
		return nil, err
	}

	return s.svc.Pipeline(r.Context(), commands, opts)
}

func splitLines(text string) ([]codec.Args, error) {
	var commands []codec.Args

	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		args, err := codec.SplitArgs(line)
		if err != nil {
			return nil, badRequest("line %d: %v", i+1, err)
		}

		commands = append(commands, args)
	}

	return commands, nil
}

// readBody reads the request body and reports whether it is JSON: either by
// Content-Type or, for clients like curl -d, by a leading '{'.
func readBody(r *http.Request) ([]byte, bool, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return nil, false, newAPIError(http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
				"request body exceeds %d bytes", maxErr.Limit)
		}

		return nil, false, badRequest("read body: %v", err)
	}

	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	isJSON := mediaType == "application/json" || bytes.HasPrefix(bytes.TrimSpace(body), []byte("{"))

	return body, isJSON, nil
}

func decodeJSON(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return badRequest("invalid JSON body: %v", err)
	}

	if dec.More() {
		return badRequest("invalid JSON body: unexpected data after the object")
	}

	return nil
}

func execOptions(r *http.Request, p execParams) (service.ExecOptions, error) {
	query := r.URL.Query()

	opts := service.ExecOptions{
		Node:     cmp.Or(p.Node, query.Get("node")),
		DB:       p.DB,
		ReadOnly: accessFromContext(r.Context()) < auth.Full,
	}

	var err error

	if opts.Target, err = service.ParseTarget(cmp.Or(p.Target, query.Get("target"))); err != nil {
		return opts, err //nolint:wrapcheck // service validation error
	}

	// node without target means target=node.
	if opts.Node != "" && p.Target == "" && query.Get("target") == "" {
		opts.Target = service.TargetNode
	}

	if opts.Encoding, err = codec.ParseEncoding(cmp.Or(p.Encoding, query.Get("encoding"))); err != nil {
		return opts, badRequest("%v", err)
	}

	if opts.DB == nil && query.Has("db") {
		db, err := strconv.Atoi(query.Get("db"))
		if err != nil {
			return opts, badRequest("invalid db %q", query.Get("db"))
		}

		opts.DB = &db
	}

	return opts, nil
}

func boolParam(r *http.Request, bodyValue *bool, name string) (bool, error) {
	if bodyValue != nil {
		return *bodyValue, nil
	}

	if !r.URL.Query().Has(name) {
		return false, nil
	}

	raw := r.URL.Query().Get(name)
	if raw == "" {
		return true, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, badRequest("invalid %s %q", name, raw)
	}

	return value, nil
}
