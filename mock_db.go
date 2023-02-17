package main

type customRule struct {
	name  string
	rule  string
	bytes []byte
}

// pretend we have a DB
type database []customRule

func (d *database) save(rules []customRule) {
	*d = rules
}

func (d *database) get() []customRule {
	return *d
}

func (d *database) getSomeAggregate(t Txn) float64 {
	if t.RiskScore > 5 {
		return 8
	}
	return 6
}

func connectDB() *database {
	return &database{}
}
