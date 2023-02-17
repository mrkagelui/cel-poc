package main

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"sort"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	expr "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
	"google.golang.org/protobuf/proto"
)

type Txn struct {
	Name          string
	Type          string
	Currency      string
	Amount        float64
	RiskScore     int
	CustomData    CustomData
	CustomBools   map[string]bool
	CustomFloats  map[string]float64
	CustomStrings map[string]string
	Aggregates    map[string]float64
}

type CustomData map[string]any

func main() {
	env, err := cel.NewEnv(
		ext.NativeTypes(reflect.TypeOf(Txn{})),
		cel.Variable("txn", cel.ObjectType("main.Txn")),
	)
	if err != nil {
		log.Fatal(err)
	}

	db := connectDB()

	if err := userSetRules(env, db); err != nil {
		log.Fatal("user set rules", err)
	}

	txns := []Txn{
		{
			Name:       "high risk QRIS large txn",
			Type:       "QRIS",
			Currency:   "IDR",
			Amount:     100000000,
			RiskScore:  8,
			CustomData: nil,
		},
		{
			Name:      "high risk VA large txn",
			Type:      "VA",
			Currency:  "IDR",
			Amount:    100000000,
			RiskScore: 7,
			CustomData: map[string]any{
				"is_vip": true,
			},
		},
		{
			Name:      "low risk VA large txn",
			Type:      "VA",
			Currency:  "IDR",
			Amount:    100000000,
			RiskScore: 2,
			CustomData: map[string]any{
				"vip_level": 7,
			},
		},
	}
	for _, txn := range txns {
		b, err := json.Marshal(txn)
		if err != nil {
			panic(err)
		}

		// to simulate the request / message parsing
		var t Txn
		if err := json.Unmarshal(b, &t); err != nil {
			panic(err)
		}
		assess(env, db, t)
	}
}

func userSetRules(env *cel.Env, db *database) error {
	type rule struct {
		name    string
		content string
	}
	rules := []rule{
		{
			name: "QRIS High risk",
			content: `txn.Type == 'QRIS'
						&& txn.Currency == 'IDR'
						&& txn.Amount >= 1000000.0
						&& txn.RiskScore >= 7`,
		},
		{
			name: "VA High risk",
			content: `txn.Type == 'VA'
						&& txn.Currency == 'IDR'
						&& txn.Amount >= 2000000.0
						&& txn.RiskScore >= 7`,
		},
		{
			name: "IDR VA Aggregate",
			content: `txn.Type == 'VA'
						&& txn.Currency == 'IDR'
						&& txn.Aggregates["failed_txn_past_month"] > 7.0`,
		},
		{
			name: "IDR VA not VIP",
			content: `txn.Type == 'VA'
						&& txn.Currency == 'IDR'
						&& !txn.CustomBools["is_vip"]`,
		},
		{
			name: "IDR VA super VIP",
			content: `txn.Type == 'VA'
						&& txn.Currency == 'IDR'
						&& txn.CustomFloats["vip_level"] > 5.0`,
		},
	}

	customRules := make([]customRule, len(rules))
	for i, r := range rules {
		ast, iss := env.Compile(r.content)
		if err := iss.Err(); err != nil {
			return fmt.Errorf("compling %v: %v", r.name, err)
		}
		if outType := ast.OutputType(); !reflect.DeepEqual(outType, cel.BoolType) {
			return fmt.Errorf("wrong output type: %v", outType)
		}
		exp, err := cel.AstToCheckedExpr(ast)
		if err != nil {
			return fmt.Errorf("to checked exp: %v", err)
		}
		b, err := proto.Marshal(exp)
		if err != nil {
			return fmt.Errorf("proto marshal: %v", err)
		}
		customRules[i] = customRule{
			name:  r.name,
			rule:  r.content,
			bytes: b,
		}
	}

	db.save(customRules)
	return nil
}

func assess(env *cel.Env, db *database, txn Txn) {
	txn = categorizeCustomData(txn)
	txn = precalculate(db, txn)

	log.Println("results for:", txn.Name)
	rules := db.get()

	wg := &sync.WaitGroup{}
	wg.Add(len(rules))

	for _, rule := range rules {
		go func(r customRule) {
			defer wg.Done()

			exp := new(expr.CheckedExpr)
			if err := proto.Unmarshal(r.bytes, exp); err != nil {
				log.Println("proto unmarshal", err)
				return
			}
			prg, err := env.Program(cel.CheckedExprToAst(exp))
			if err != nil {
				log.Println("build", r.name, err)
				return
			}
			out, det, err := prg.Eval(map[string]any{"txn": txn})
			if err != nil {
				log.Println("eval", r.name, err)
				return
			}
			log.Printf("rule [%v] result: %v (%T)\n", r.name, out, out)
			if det != nil {
				log.Printf("------ eval states ------")
				state := det.State()
				stateIDs := state.IDs()
				ids := make([]int, len(stateIDs), len(stateIDs))
				for i, id := range stateIDs {
					ids[i] = int(id)
				}
				sort.Ints(ids)
				for _, id := range ids {
					v, found := state.Value(int64(id))
					if !found {
						continue
					}
					log.Printf("%d: %v (%T)\n", id, v, v)
				}
			}
		}(rule)
	}

	wg.Wait()
	log.Println()
}

func categorizeCustomData(txn Txn) Txn {
	result := txn
	if result.CustomBools == nil {
		result.CustomBools = make(map[string]bool, len(txn.CustomData))
	}
	if result.CustomFloats == nil {
		result.CustomFloats = make(map[string]float64, len(txn.CustomData))
	}
	if result.CustomStrings == nil {
		result.CustomStrings = make(map[string]string, len(txn.CustomData))
	}
	for k, v := range txn.CustomData {
		switch v.(type) {
		case int:
			result.CustomFloats[k] = float64(v.(int))
		case float64:
			result.CustomFloats[k] = v.(float64)
		case bool:
			result.CustomBools[k] = v.(bool)
		case string:
			result.CustomStrings[k] = v.(string)
		}
		// other types are not supported
	}
	return result
}

func precalculate(db *database, txn Txn) Txn {
	if txn.Aggregates == nil {
		txn.Aggregates = make(map[string]float64)
	}
	txn.Aggregates["failed_txn_past_month"] = db.getSomeAggregate(txn)
	return txn
}
