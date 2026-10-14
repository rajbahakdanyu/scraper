package cmd

func runBatch(opts globalOptions, entries []batchEntry) error {
	cfg, err := configLoader(opts)
	if err != nil {
		return err
	}
	return runBatchWithConfig(cfg, opts, entries)
}
