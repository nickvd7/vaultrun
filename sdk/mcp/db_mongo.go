// MongoDB MCP tool handlers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// realMongoDB wraps *mongo.Database and satisfies mongoDBHandle.
type realMongoDB struct{ db *mongo.Database }

func (r *realMongoDB) mongoHandle() {}

// openMongoDB connects to MongoDB and returns a mongoDBHandle.
func openMongoDB(ctx context.Context, uri, dbName string) (mongoDBHandle, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return &realMongoDB{db: client.Database(dbName)}, nil
}

func (s *server) mongoOrErr() (*mongo.Database, error) {
	if s.db == nil || s.db.mongoDB == nil {
		return nil, fmt.Errorf("MongoDB not configured — set MCP_MONGO_URI and MCP_MONGO_DB")
	}
	r, ok := s.db.mongoDB.(*realMongoDB)
	if !ok {
		return nil, fmt.Errorf("MongoDB handle type assertion failed")
	}
	return r.db, nil
}

// bsonToJSON renders a bson.Raw document as indented JSON.
func bsonToJSON(raw bson.Raw) string {
	var m bson.M
	if err := bson.Unmarshal(raw, &m); err != nil {
		return fmt.Sprintf("(unmarshal error: %v)", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Sprintf("(marshal error: %v)", err)
	}
	return string(b)
}

// parseFilter parses a JSON filter string into bson.D.
// An empty or missing filter defaults to an empty document (match all).
func parseFilter(raw string) (bson.D, error) {
	if strings.TrimSpace(raw) == "" {
		return bson.D{}, nil
	}
	var d bson.D
	if err := bson.UnmarshalExtJSON([]byte(raw), true, &d); err != nil {
		return nil, fmt.Errorf("invalid filter JSON: %w", err)
	}
	return d, nil
}

// parseDocument parses a JSON string into bson.D.
func parseDocument(raw string) (bson.D, error) {
	if strings.TrimSpace(raw) == "" {
		return bson.D{}, nil
	}
	var d bson.D
	if err := bson.UnmarshalExtJSON([]byte(raw), true, &d); err != nil {
		return nil, fmt.Errorf("invalid document JSON: %w", err)
	}
	return d, nil
}

func (s *server) toolMongoFind(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	db, err := s.mongoOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}
	collection := args["collection"]
	if collection == "" {
		return mcpToolResult{}, fmt.Errorf("collection is required")
	}
	filter, err := parseFilter(args["filter"])
	if err != nil {
		return mcpToolResult{}, err
	}
	limit := int64(20)
	if v := args["limit"]; v != "" {
		fmt.Sscanf(v, "%d", &limit)
		if limit <= 0 || limit > 1000 {
			limit = 20
		}
	}

	opts := options.Find().SetLimit(limit)
	cur, err := db.Collection(collection).Find(ctx, filter, opts)
	if err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	defer cur.Close(ctx)

	var sb strings.Builder
	count := 0
	for cur.Next(ctx) {
		sb.WriteString(bsonToJSON(cur.Current))
		sb.WriteByte('\n')
		count++
	}
	if err := cur.Err(); err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	if count == 0 {
		return textResult("No documents found."), nil
	}
	fmt.Fprintf(&sb, "(%d document(s))\n", count)
	return textResult(sb.String()), nil
}

func (s *server) toolMongoInsertOne(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	db, err := s.mongoOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}
	collection := args["collection"]
	if collection == "" {
		return mcpToolResult{}, fmt.Errorf("collection is required")
	}
	docStr := args["document"]
	if docStr == "" {
		return mcpToolResult{}, fmt.Errorf("document is required")
	}
	doc, err := parseDocument(docStr)
	if err != nil {
		return mcpToolResult{}, err
	}
	res, err := db.Collection(collection).InsertOne(ctx, doc)
	if err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	return textResult(fmt.Sprintf("Inserted document with _id: %v", res.InsertedID)), nil
}

func (s *server) toolMongoUpdate(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	db, err := s.mongoOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}
	collection := args["collection"]
	if collection == "" {
		return mcpToolResult{}, fmt.Errorf("collection is required")
	}
	filter, err := parseFilter(args["filter"])
	if err != nil {
		return mcpToolResult{}, err
	}
	updateStr := args["update"]
	if updateStr == "" {
		return mcpToolResult{}, fmt.Errorf("update is required")
	}
	update, err := parseDocument(updateStr)
	if err != nil {
		return mcpToolResult{}, err
	}
	many := args["many"] == "true"
	var matched, modified int64
	if many {
		res, err := db.Collection(collection).UpdateMany(ctx, filter, update)
		if err != nil {
			return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
		}
		matched, modified = res.MatchedCount, res.ModifiedCount
	} else {
		res, err := db.Collection(collection).UpdateOne(ctx, filter, update)
		if err != nil {
			return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
		}
		matched, modified = res.MatchedCount, res.ModifiedCount
	}
	return textResult(fmt.Sprintf("matched=%d, modified=%d", matched, modified)), nil
}

func (s *server) toolMongoDelete(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	db, err := s.mongoOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}
	collection := args["collection"]
	if collection == "" {
		return mcpToolResult{}, fmt.Errorf("collection is required")
	}
	filter, err := parseFilter(args["filter"])
	if err != nil {
		return mcpToolResult{}, err
	}
	many := args["many"] == "true"
	var deleted int64
	if many {
		res, err := db.Collection(collection).DeleteMany(ctx, filter)
		if err != nil {
			return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
		}
		deleted = res.DeletedCount
	} else {
		res, err := db.Collection(collection).DeleteOne(ctx, filter)
		if err != nil {
			return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
		}
		deleted = res.DeletedCount
	}
	return textResult(fmt.Sprintf("deleted=%d", deleted)), nil
}

func (s *server) toolMongoAggregate(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	db, err := s.mongoOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}
	collection := args["collection"]
	if collection == "" {
		return mcpToolResult{}, fmt.Errorf("collection is required")
	}
	pipelineStr := args["pipeline"]
	if pipelineStr == "" {
		return mcpToolResult{}, fmt.Errorf("pipeline is required (JSON array)")
	}

	var pipeline bson.A
	if err := bson.UnmarshalExtJSON([]byte(pipelineStr), true, &pipeline); err != nil {
		return mcpToolResult{}, fmt.Errorf("invalid pipeline JSON: %w", err)
	}

	cur, err := db.Collection(collection).Aggregate(ctx, pipeline)
	if err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	defer cur.Close(ctx)

	var sb strings.Builder
	count := 0
	for cur.Next(ctx) {
		sb.WriteString(bsonToJSON(cur.Current))
		sb.WriteByte('\n')
		count++
	}
	if err := cur.Err(); err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	fmt.Fprintf(&sb, "(%d result(s))\n", count)
	return textResult(sb.String()), nil
}

func (s *server) toolMongoCollections(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	db, err := s.mongoOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}
	names, err := db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	if len(names) == 0 {
		return textResult("No collections found."), nil
	}
	return textResult(strings.Join(names, "\n")), nil
}

// toolMongoGenerateMongoose samples up to 50 documents from the given
// collection, infers field types from the BSON data, and generates a
// Mongoose schema as JavaScript code.
func (s *server) toolMongoGenerateMongoose(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	db, err := s.mongoOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}
	collection := args["collection"]
	if collection == "" {
		return mcpToolResult{}, fmt.Errorf("collection is required")
	}

	cur, err := db.Collection(collection).Find(ctx, bson.D{}, options.Find().SetLimit(50))
	if err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	defer cur.Close(ctx)

	// Collect all field names + BSON type hints across sampled documents.
	type fieldInfo struct {
		bsonType string // first observed BSON type name
		nullable bool
	}
	fields := map[string]*fieldInfo{}
	fieldOrder := []string{}
	seenDoc := 0

	for cur.Next(ctx) {
		var m bson.M
		if err := bson.Unmarshal(cur.Current, &m); err != nil {
			continue
		}
		seenDoc++
		for k, v := range m {
			if k == "_id" {
				continue
			}
			if _, exists := fields[k]; !exists {
				fieldOrder = append(fieldOrder, k)
				fields[k] = &fieldInfo{bsonType: inferMongooseType(v)}
			} else if v == nil {
				fields[k].nullable = true
			}
		}
	}
	if err := cur.Err(); err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	if seenDoc == 0 {
		return textResult("Collection is empty — cannot infer schema."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "// Auto-generated Mongoose schema for collection %q\n", collection)
	fmt.Fprintf(&sb, "// Sampled from %d document(s)\n\n", seenDoc)
	fmt.Fprintf(&sb, "const mongoose = require('mongoose');\nconst { Schema } = mongoose;\n\n")
	fmt.Fprintf(&sb, "const %sSchema = new Schema({\n", toCamel(collection))
	for _, k := range fieldOrder {
		fi := fields[k]
		nullable := ""
		if fi.nullable {
			nullable = " // nullable"
		}
		fmt.Fprintf(&sb, "  %s: { type: %s },%s\n", k, fi.bsonType, nullable)
	}
	fmt.Fprintf(&sb, "}, { timestamps: true });\n\n")
	fmt.Fprintf(&sb, "module.exports = mongoose.model('%s', %sSchema);\n",
		capitalize(collection), toCamel(collection))

	return textResult(sb.String()), nil
}

// inferMongooseType maps a BSON value to a Mongoose/JS type string.
func inferMongooseType(v any) string {
	switch v.(type) {
	case int32, int64, float64:
		return "Number"
	case bool:
		return "Boolean"
	case bson.A:
		return "[Schema.Types.Mixed]"
	case bson.M, bson.D:
		return "Schema.Types.Mixed"
	case nil:
		return "Schema.Types.Mixed"
	default:
		return "String"
	}
}

// toCamel converts "my_collection" to "myCollection".
func toCamel(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return s
	}
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// capitalize uppercases the first rune of s.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
