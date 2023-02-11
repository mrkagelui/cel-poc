package main

import (
	"fmt"
	"log"
	"reflect"
	"sort"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
)

type Txn struct {
	Name       string
	Type       string
	Currency   string
	Amount     float64
	RiskScore  int
	CustomData any
}

type customRule struct {
	name string
	ast  *cel.Ast
}

// pretend we have a DB
type database []customRule

func (d *database) save(rules []customRule) {
	*d = rules
}

func (d *database) get() []customRule {
	return *d
}

func connectDB() *database {
	return &database{}
}

func main() {
	env, err := cel.NewEnv(
		ext.NativeTypes(reflect.TypeOf(Txn{})),
		cel.Variable("txn", cel.ObjectType("main.Txn")),
	)
	if err != nil {
		log.Panicln(err)
	}

	db := connectDB()

	if err := userSetRules(env, db); err != nil {
		log.Panicln("user set rules", err)
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
			Name:       "high risk VA large txn",
			Type:       "VA",
			Currency:   "IDR",
			Amount:     100000000,
			RiskScore:  7,
			CustomData: nil,
		},
		{
			Name:       "low risk VA large txn",
			Type:       "VA",
			Currency:   "IDR",
			Amount:     100000000,
			RiskScore:  2,
			CustomData: nil,
		},
	}
	for _, txn := range txns {
		assess(env, db, txn)
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
		customRules[i] = customRule{
			name: r.name,
			ast:  ast,
		}
	}

	db.save(customRules)
	return nil
}

func assess(env *cel.Env, db *database, txn Txn) {
	log.Println("results for:", txn.Name)
	rules := db.get()

	wg := &sync.WaitGroup{}
	wg.Add(len(rules))

	for _, rule := range rules {
		go func(r customRule) {
			defer wg.Done()

			prg, err := env.Program(r.ast)
			if err != nil {
				log.Println("build", r.name, err)
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
