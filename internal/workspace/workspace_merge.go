package workspace

func mergeAPIConfig(base, overlay APIConfig) APIConfig {
	res := CopyAPIConfig(base)
	if overlay.Protocol != "" {
		res.Protocol = overlay.Protocol
	}
	if overlay.Mode != "" {
		res.Mode = overlay.Mode
	}
	if overlay.Timeout != "" {
		res.Timeout = overlay.Timeout
	}
	if overlay.URL != "" {
		res.URL = overlay.URL
	}
	if overlay.Headers != nil {
		if res.Headers == nil {
			res.Headers = make(map[string]string)
		}
		for k, v := range overlay.Headers {
			res.Headers[k] = v
		}
	}
	if overlay.ExpectedRequest != nil {
		res.ExpectedRequest = overlay.ExpectedRequest
	}
	if overlay.ExpectedHeaders != nil {
		res.ExpectedHeaders = overlay.ExpectedHeaders
	}
	if overlay.ExpectedResponse != nil {
		res.ExpectedResponse = overlay.ExpectedResponse
	}
	if overlay.DefaultResponse != nil {
		res.DefaultResponse = overlay.DefaultResponse
	}
	return res
}

func mergeEntrypointConfig(base, overlay EntrypointConfig) EntrypointConfig {
	res := CopyEntrypointConfig(base)
	if overlay.Type != "" {
		res.Type = overlay.Type
	}
	if overlay.Method != "" {
		res.Method = overlay.Method
	}
	if overlay.Path != "" {
		res.Path = overlay.Path
	}
	if overlay.URL != "" {
		res.URL = overlay.URL
	}
	if overlay.Headers != nil {
		if res.Headers == nil {
			res.Headers = make(map[string]string)
		}
		for k, v := range overlay.Headers {
			res.Headers[k] = v
		}
	}
	if overlay.FollowRedirects != nil {
		res.FollowRedirects = overlay.FollowRedirects
	}
	if overlay.ExpectedResponse != nil {
		res.ExpectedResponse = overlay.ExpectedResponse
	}
	return res
}
