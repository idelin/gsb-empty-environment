package netsec

// Fixture schema used across the admission and execution tests.
//
//	interface Node { id: ID }
//	type User implements Node {
//	  id: ID; name: String (read:profile); email: String (read:email)
//	  posts: [Post!]! @max(len: 3) @cost(5)
//	  friends: [User!]! @max(len: 2) @cost(4)
//	}
//	type Post implements Node {
//	  id: ID; title: String @cost(2)
//	  author: User @cost(3)
//	  comments: [Comment!]! @max(len: 2) @cost(4)
//	  batches: [[String!]!]! @max(len: [2, 3]) @cost(1)
//	}
//	type Comment implements Node { id: ID; body: String; author: User @cost(3) }
//	union SearchResult = User | Post
//	type Query {
//	  me: User @cost(1); user(id: ID!): User @cost(2)
//	  posts: [Post!]! @max(len: 3) @cost(5)
//	  search(term: String!): [SearchResult!]! @max(len: 4) @cost(6)
//	  node(id: ID!): Node @cost(2)
//	}
//	type Mutation { createPost(title: String!): Post @cost(10) (write:post) }
func fixtureSchema() *Schema {
	stringType := &Type{Kind: KindScalar, Name: "String"}
	idType := &Type{Kind: KindScalar, Name: "ID"}

	userType := &Type{Kind: KindObject, Name: "User", Fields: map[string]*FieldDef{}}
	postType := &Type{Kind: KindObject, Name: "Post", Fields: map[string]*FieldDef{}}
	commentType := &Type{Kind: KindObject, Name: "Comment", Fields: map[string]*FieldDef{}}

	nodeType := &Type{
		Kind:          KindInterface,
		Name:          "Node",
		PossibleTypes: []string{"User", "Post", "Comment"},
		Fields: map[string]*FieldDef{
			"id": {Name: "id", Type: idType, Cost: 1},
		},
	}
	searchResult := &Type{
		Kind:          KindUnion,
		Name:          "SearchResult",
		PossibleTypes: []string{"User", "Post"},
	}

	userType.Fields = map[string]*FieldDef{
		"id":      {Name: "id", Type: idType, Cost: 1},
		"name":    {Name: "name", Type: stringType, Cost: 1, Scope: "read:profile"},
		"email":   {Name: "email", Type: stringType, Cost: 1, Scope: "read:email"},
		"posts":   {Name: "posts", Type: postType, ListMaxLen: []int{3}, Cost: 5},
		"friends": {Name: "friends", Type: userType, ListMaxLen: []int{2}, Cost: 4},
	}
	postType.Fields = map[string]*FieldDef{
		"id":       {Name: "id", Type: idType, Cost: 1},
		"title":    {Name: "title", Type: stringType, Cost: 2},
		"author":   {Name: "author", Type: userType, Cost: 3},
		"comments": {Name: "comments", Type: commentType, ListMaxLen: []int{2}, Cost: 4},
		"batches":  {Name: "batches", Type: stringType, ListMaxLen: []int{2, 3}, Cost: 1},
	}
	commentType.Fields = map[string]*FieldDef{
		"id":     {Name: "id", Type: idType, Cost: 1},
		"body":   {Name: "body", Type: stringType, Cost: 1},
		"author": {Name: "author", Type: userType, Cost: 3},
	}

	queryType := &Type{
		Kind: KindObject,
		Name: "Query",
		Fields: map[string]*FieldDef{
			"me":     {Name: "me", Type: userType, Cost: 1},
			"user":   {Name: "user", Type: userType, Cost: 2},
			"posts":  {Name: "posts", Type: postType, ListMaxLen: []int{3}, Cost: 5},
			"search": {Name: "search", Type: searchResult, ListMaxLen: []int{4}, Cost: 6},
			"node":   {Name: "node", Type: nodeType, Cost: 2},
		},
	}
	mutationType := &Type{
		Kind: KindObject,
		Name: "Mutation",
		Fields: map[string]*FieldDef{
			"createPost": {
				Name:  "createPost",
				Type:  postType,
				Cost:  10,
				Scope: "write:post",
			},
		},
	}

	return &Schema{
		Types: map[string]*Type{
			"Query":        queryType,
			"Mutation":     mutationType,
			"User":         userType,
			"Post":         postType,
			"Comment":      commentType,
			"Node":         nodeType,
			"SearchResult": searchResult,
			"String":       stringType,
			"ID":           idType,
		},
		QueryType:    "Query",
		MutationType: "Mutation",
	}
}

func field(name string, children ...Selection) *Field {
	return &Field{Name: name, SelectionSet: children}
}

func aliased(alias, name string, children ...Selection) *Field {
	return &Field{Alias: alias, Name: name, SelectionSet: children}
}

func arg(name string, value Value) *Argument {
	return &Argument{Name: name, Value: value}
}

func spread(name string) *FragmentSpread {
	return &FragmentSpread{Name: name}
}

func inline(typeName string, children ...Selection) *InlineFragment {
	return &InlineFragment{TypeCondition: typeName, SelectionSet: children}
}

func skipDirective(ifValue Value) *Directive {
	return &Directive{Name: "skip", Arguments: []*Argument{arg("if", ifValue)}}
}

func includeDirective(ifValue Value) *Directive {
	return &Directive{Name: "include", Arguments: []*Argument{arg("if", ifValue)}}
}

func singleDoc(selections ...Selection) *Document {
	return &Document{
		Operations: []*Operation{{Type: OpQuery, SelectionSet: selections}},
		Fragments:  map[string]*FragmentDefinition{},
	}
}

func admitQuery(schema *Schema, doc *Document, budget int64, scopes ...string) (*Report, error) {
	granted := map[string]bool{}
	for _, scope := range scopes {
		granted[scope] = true
	}
	return Admit(Options{
		Schema:        schema,
		Document:      doc,
		Variables:     map[string]any{},
		GrantedScopes: granted,
		Budget:        budget,
	})
}
