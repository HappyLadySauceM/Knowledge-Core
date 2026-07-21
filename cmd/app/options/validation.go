package options

import "errors"

func (o *Options) Validate() error {
	var err error
	err = errors.Join(err, o.InsecureServing.Validate())
	err = errors.Join(err, o.Database.Validate())
	err = errors.Join(err, o.Redis.Validate())
	err = errors.Join(err, o.JWT.Validate())
	err = errors.Join(err, o.WebSocket.Validate())
	err = errors.Join(err, o.Uploads.Validate())
	return err
}
