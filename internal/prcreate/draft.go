package prcreate

func resolveDraft(opts Options, cfg FileConfig) bool {
	if opts.DraftGiven {
		return opts.Draft
	}
	if cfg.Create.Draft != nil {
		return *cfg.Create.Draft
	}
	return false
}
