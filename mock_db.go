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

func connectDB() *database {
	return &database{}
}
