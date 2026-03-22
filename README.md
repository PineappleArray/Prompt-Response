# Prompt-Response
An application that focuses on helping users get the most out of their tokens utilizing a vLLM as well as a router that will balance the tokens taken up with the quality and specializations of the LLM models

Portfolio Grade Compromises: 
No Crash Checking: If a vLLM crashes it will still recieve signals

No Request Lifecycle: There a no timeouts for requests a hung vLLM will remain hung

No Autoscaling: Due to the dockers testing it will result in an inability to scale horizantally

No Ratelimiting: There are no limiting for clients if they take up a large amount of resources

Partial Redis Resilience: Redis server can go down and there is no backup and only periodic writes

Partial Router Scalability: Currently there is a single router
