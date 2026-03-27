# Prompt-Response
An application that focuses on helping users get the most out of their tokens utilizing a vLLM as well as a router that will balance the tokens taken up with the quality and specializations of the LLM models

Portfolio Grade Compromises: 
No Crash Checking: If a vLLM crashes it will still recieve signals

No Request Lifecycle: There a no timeouts for requests a hung vLLM will remain hung

No Autoscaling: Due to the dockers testing it will result in an inability to scale horizantally

No Ratelimiting: There are no limiting for clients if they take up a large amount of resources

Partial Redis Resilience: Redis server can go down and there is no backup and only periodic writes

Partial Router Scalability: Currently there is a single router

Current Layout:
There are currently 5 layers in this project:
```
Layer 1 Proxy Handler (handler.go):
Processes the Request
type Request struct {
	SystemPrompt string 
	UserMessage  string 
	TokenCount   int    
	HasCode      bool  
	ConvTurns    int 
}
Sends Response to classifier
type Response struct {
	Tier    ModelTier       
	Score   float64          
	Signals map[string]float64 
	Reason  string            
}
          |
          |
          V
Layer 2 Classifier (heuristic.go):
Takes the reponse and utilizes a lightweight model (WIP)
to not only determine which model should be used for what purpose
but as well what size. Currently the algorithm that is utilized is 
based on 4 metrics prompt length, if there is code, reasoning and,
complexity.
          |
          |
          V
Layer 3 Replica (scorer.go):
Takes the hash and checks if a similar prompt has been ran and saved
into the store. Then it checks which of the replicas are available to be used.
          |
          |
          V
Layer 4
```
