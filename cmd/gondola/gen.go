package main

import (
	"gnd.la/admin"
	"gnd.la/gen"
	"gnd.la/mux"
)

func Gen(ctx *mux.Context) {
	var genfile string
	ctx.ParseParamValue("genfile", &genfile)
	if err := gen.Gen(".", genfile); err != nil {
		panic(err)
	}
}

func init() {
	admin.Register(Gen, &admin.Options{
		Help: "Perform code generation in the current directory according the rules in the config file",
		Flags: admin.Flags(
			admin.StringFlag("genfile", "genfile.yaml", "Code generation configuration file"),
		),
	})
}