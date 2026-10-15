package lib

type Client struct{}

func (*Client) Ping() {}

func NewClient() *Client { return &Client{} }
