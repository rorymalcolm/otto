package parser

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/robertkrimen/otto/ast"
	"github.com/robertkrimen/otto/file"
	"github.com/robertkrimen/otto/token"
)

func (p *parser) parseIdentifier() *ast.Identifier {
	literal := p.literal
	idx := p.idx
	if p.mode&StoreComments != 0 {
		p.comments.MarkComments(ast.LEADING)
	}
	p.next()
	exp := &ast.Identifier{
		Name: literal,
		Idx:  idx,
	}

	if p.mode&StoreComments != 0 {
		p.comments.SetExpression(exp)
	}

	return exp
}

func (p *parser) parsePrimaryExpression() ast.Expression {
	literal := p.literal
	idx := p.idx
	switch p.token {
	case token.IDENTIFIER:
		p.next()
		if len(literal) > 1 {
			tkn, strict := token.IsKeyword(literal)
			if tkn == token.KEYWORD {
				if !strict {
					p.error(idx, "Unexpected reserved word")
				}
			}
		}
		identifier := &ast.Identifier{
			Name: literal,
			Idx:  idx,
		}
		if p.token == token.ARROW {
			return p.parseArrowFunction(idx, []*ast.Identifier{identifier}, idx, identifier.Idx1())
		}
		return identifier
	case token.NULL:
		p.next()
		return &ast.NullLiteral{
			Idx:     idx,
			Literal: literal,
		}
	case token.BOOLEAN:
		p.next()
		value := false
		switch literal {
		case "true":
			value = true
		case "false":
			value = false
		default:
			p.error(idx, "Illegal boolean literal")
		}
		return &ast.BooleanLiteral{
			Idx:     idx,
			Literal: literal,
			Value:   value,
		}
	case token.STRING:
		p.next()
		value, err := parseStringLiteral(literal[1 : len(literal)-1])
		if err != nil {
			p.error(idx, err.Error())
		}
		return &ast.StringLiteral{
			Idx:     idx,
			Literal: literal,
			Value:   value,
		}
	case token.TEMPLATE:
		return p.parseTemplateLiteral(idx, literal)
	case token.NUMBER:
		p.next()
		value, err := parseNumberLiteral(literal)
		if err != nil {
			p.error(idx, err.Error())
			value = 0
		}
		return &ast.NumberLiteral{
			Idx:     idx,
			Literal: literal,
			Value:   value,
		}
	case token.SLASH, token.QUOTIENT_ASSIGN:
		return p.parseRegExpLiteral()
	case token.LEFT_BRACE:
		return p.parseObjectLiteral()
	case token.LEFT_BRACKET:
		return p.parseArrayLiteral()
	case token.LEFT_PARENTHESIS:
		opening := p.expect(token.LEFT_PARENTHESIS)
		if p.token == token.RIGHT_PARENTHESIS {
			// Either an empty arrow parameter list, "() => ...", or a
			// syntax error (an empty parenthesised expression).
			closing := p.expect(token.RIGHT_PARENTHESIS)
			if p.token == token.ARROW {
				return p.parseArrowFunction(opening, nil, opening, closing)
			}
			p.error(opening, "Unexpected token )")
			return &ast.BadExpression{From: opening, To: closing}
		}
		expression := p.parseExpression()
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		closing := p.expect(token.RIGHT_PARENTHESIS)
		if p.token == token.ARROW {
			list, ok := arrowParameterList(expression)
			if !ok {
				p.error(expression.Idx0(), "malformed arrow function parameter list")
				return &ast.BadExpression{From: opening, To: p.idx}
			}
			return p.parseArrowFunction(opening, list, opening, closing)
		}
		return expression
	case token.THIS:
		p.next()
		return &ast.ThisExpression{
			Idx: idx,
		}
	case token.FUNCTION:
		return p.parseFunction(false)
	}

	p.errorUnexpectedToken(p.token)
	p.nextStatement()
	return &ast.BadExpression{From: idx, To: p.idx}
}

// parseTemplateLiteral builds a TemplateLiteral node from the raw template
// source (including the enclosing backticks). It splits the literal into cooked
// string segments and embedded ${ ... } expressions, parsing each embedded
// expression with a sub-parser.
func (p *parser) parseTemplateLiteral(idx file.Idx, literal string) ast.Expression {
	closeQuote := file.Idx(int(idx) + len(literal) - 1)
	p.next()

	node := &ast.TemplateLiteral{
		OpenQuote:  idx,
		CloseQuote: closeQuote,
	}

	// Strip the enclosing backticks.
	inner := ""
	if len(literal) >= 2 {
		inner = literal[1 : len(literal)-1]
	}

	var cooked strings.Builder
	i := 0
	for i < len(inner) {
		switch {
		case inner[i] == '\\':
			i = cookTemplateEscape(&cooked, inner, i+1)
		case inner[i] == '$' && i+1 < len(inner) && inner[i+1] == '{':
			node.Strings = append(node.Strings, cooked.String())
			cooked.Reset()
			src, next, err := extractTemplateSubstitution(inner, i+2)
			if err != nil {
				p.error(idx, err.Error())
				return &ast.BadExpression{From: idx, To: closeQuote}
			}
			node.Expressions = append(node.Expressions, p.parseTemplateExpression(src, idx))
			i = next
		default:
			cooked.WriteByte(inner[i])
			i++
		}
	}
	node.Strings = append(node.Strings, cooked.String())

	return node
}

// parseTemplateExpression parses the source of a single ${ ... } substitution
// into an expression using a sub-parser.
func (p *parser) parseTemplateExpression(src string, idx file.Idx) ast.Expression {
	if strings.TrimSpace(src) == "" {
		p.error(idx, "unexpected token in template literal")
		return &ast.BadExpression{From: idx, To: idx}
	}

	program, err := ParseFile(nil, "", "("+src+"\n)", 0)
	if err != nil {
		p.error(idx, "invalid template substitution: %s", err.Error())
		return &ast.BadExpression{From: idx, To: idx}
	}

	stmt, ok := program.Body[0].(*ast.ExpressionStatement)
	if !ok || stmt.Expression == nil {
		p.error(idx, "invalid template substitution")
		return &ast.BadExpression{From: idx, To: idx}
	}

	return stmt.Expression
}

// extractTemplateSubstitution returns the source of a ${ ... } substitution
// beginning at start (just past the opening brace) and the index just past its
// matching closing brace. Nested braces, string literals and nested template
// literals are skipped so their contents do not terminate the substitution.
func extractTemplateSubstitution(s string, start int) (string, int, error) {
	depth := 1
	i := start
	for i < len(s) {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start:i], i + 1, nil
			}
		case '\\':
			i++ // skip the escaped character
		case '`':
			j, err := skipNestedTemplate(s, i+1)
			if err != nil {
				return "", 0, err
			}
			i = j
			continue
		case '\'', '"':
			j, err := skipNestedString(s, i)
			if err != nil {
				return "", 0, err
			}
			i = j
			continue
		}
		i++
	}
	return "", 0, errInvalidTemplate
}

// skipNestedTemplate returns the index just past the closing backtick of a
// template literal whose body begins at i.
func skipNestedTemplate(s string, i int) (int, error) {
	for i < len(s) {
		switch s[i] {
		case '`':
			return i + 1, nil
		case '\\':
			i++
		case '$':
			if i+1 < len(s) && s[i+1] == '{' {
				j, err := extractNestedSubstitution(s, i+2)
				if err != nil {
					return 0, err
				}
				i = j
				continue
			}
		}
		i++
	}
	return 0, errInvalidTemplate
}

// extractNestedSubstitution is like extractTemplateSubstitution but returns
// only the index past the closing brace.
func extractNestedSubstitution(s string, start int) (int, error) {
	_, next, err := extractTemplateSubstitution(s, start)
	return next, err
}

// skipNestedString returns the index just past the closing quote of a string
// literal whose opening quote is at i.
func skipNestedString(s string, i int) (int, error) {
	quote := s[i]
	i++
	for i < len(s) {
		switch s[i] {
		case quote:
			return i + 1, nil
		case '\\':
			i++
		}
		i++
	}
	return 0, errInvalidTemplate
}

// cookTemplateEscape interprets the escape sequence whose character follows the
// backslash at s[i], writing the cooked result to b, and returns the index just
// past the consumed escape.
func cookTemplateEscape(b *strings.Builder, s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch c := s[i]; c {
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'v':
		b.WriteByte('\v')
	case '0':
		b.WriteByte(0)
	case 'x':
		if i+3 <= len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 32); err == nil {
				b.WriteRune(rune(v))
				return i + 3
			}
		}
		b.WriteByte(c)
	case 'u':
		if i+1 < len(s) && s[i+1] == '{' {
			if end := strings.IndexByte(s[i+2:], '}'); end >= 0 {
				if v, err := strconv.ParseUint(s[i+2:i+2+end], 16, 32); err == nil {
					b.WriteRune(rune(v))
					return i + 2 + end + 1
				}
			}
		} else if i+5 <= len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+5], 16, 32); err == nil {
				b.WriteRune(rune(v))
				return i + 5
			}
		}
		b.WriteByte(c)
	case '\n':
		return i + 1 // line continuation
	case '\r':
		if i+1 < len(s) && s[i+1] == '\n' {
			return i + 2
		}
		return i + 1
	default:
		b.WriteByte(c)
	}
	return i + 1
}

func (p *parser) parseRegExpLiteral() *ast.RegExpLiteral {
	offset := p.chrOffset - 1 // Opening slash already gotten
	if p.token == token.QUOTIENT_ASSIGN {
		offset-- // =
	}
	idx := p.idxOf(offset)

	pattern, err := p.scanString(offset)
	endOffset := p.chrOffset

	p.next()
	if err == nil {
		pattern = pattern[1 : len(pattern)-1]
	}

	flags := ""
	if p.token == token.IDENTIFIER { // gim
		flags = p.literal
		endOffset = p.chrOffset
		p.next()
	}

	var value string
	// TODO 15.10
	// Test during parsing that this is a valid regular expression
	// Sorry, (?=) and (?!) are invalid (for now)
	pat, err := TransformRegExp(pattern)
	if err != nil {
		if pat == "" || p.mode&IgnoreRegExpErrors == 0 {
			p.error(idx, "Invalid regular expression: %s", err.Error())
		}
	} else {
		_, err = regexp.Compile(pat)
		if err != nil {
			// We should not get here, ParseRegExp should catch any errors
			p.error(idx, "Invalid regular expression: %s", err.Error()[22:]) // Skip redundant "parse regexp error"
		} else {
			value = pat
		}
	}

	literal := p.str[offset:endOffset]

	return &ast.RegExpLiteral{
		Idx:     idx,
		Literal: literal,
		Pattern: pattern,
		Flags:   flags,
		Value:   value,
	}
}

func (p *parser) parseVariableDeclaration(declarationList *[]*ast.VariableExpression) ast.Expression {
	if p.token != token.IDENTIFIER {
		idx := p.expect(token.IDENTIFIER)
		p.nextStatement()
		return &ast.BadExpression{From: idx, To: p.idx}
	}

	literal := p.literal
	idx := p.idx
	p.next()
	node := &ast.VariableExpression{
		Name: literal,
		Idx:  idx,
	}
	if p.mode&StoreComments != 0 {
		p.comments.SetExpression(node)
	}

	if declarationList != nil {
		*declarationList = append(*declarationList, node)
	}

	if p.token == token.ASSIGN {
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()
		node.Initializer = p.parseAssignmentExpression()
	}

	return node
}

func (p *parser) parseVariableDeclarationList(idx file.Idx) []ast.Expression {
	var declarationList []*ast.VariableExpression // Avoid bad expressions
	var list []ast.Expression

	for {
		if p.mode&StoreComments != 0 {
			p.comments.MarkComments(ast.LEADING)
		}
		decl := p.parseVariableDeclaration(&declarationList)
		list = append(list, decl)
		if p.token != token.COMMA {
			break
		}
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()
	}

	p.scope.declare(&ast.VariableDeclaration{
		Var:  idx,
		List: declarationList,
	})

	return list
}

func (p *parser) parseObjectPropertyKey() (string, string) {
	idx, tkn, literal := p.idx, p.token, p.literal
	value := ""
	if p.mode&StoreComments != 0 {
		p.comments.MarkComments(ast.KEY)
	}
	p.next()

	switch tkn {
	case token.IDENTIFIER:
		value = literal
	case token.NUMBER:
		var err error
		_, err = parseNumberLiteral(literal)
		if err != nil {
			p.error(idx, err.Error())
		} else {
			value = literal
		}
	case token.STRING:
		var err error
		value, err = parseStringLiteral(literal[1 : len(literal)-1])
		if err != nil {
			p.error(idx, err.Error())
		}
	default:
		// null, false, class, etc.
		if matchIdentifier.MatchString(literal) {
			value = literal
		}
	}
	return literal, value
}

func (p *parser) parseObjectProperty() ast.Property {
	literal, value := p.parseObjectPropertyKey()
	if literal == "get" && p.token != token.COLON {
		idx := p.idx
		_, value = p.parseObjectPropertyKey()
		parameterList := p.parseFunctionParameterList()

		node := &ast.FunctionLiteral{
			Function:      idx,
			ParameterList: parameterList,
		}
		p.parseFunctionBlock(node)
		return ast.Property{
			Key:   value,
			Kind:  "get",
			Value: node,
		}
	} else if literal == "set" && p.token != token.COLON {
		idx := p.idx
		_, value = p.parseObjectPropertyKey()
		parameterList := p.parseFunctionParameterList()

		node := &ast.FunctionLiteral{
			Function:      idx,
			ParameterList: parameterList,
		}
		p.parseFunctionBlock(node)
		return ast.Property{
			Key:   value,
			Kind:  "set",
			Value: node,
		}
	}

	if p.mode&StoreComments != 0 {
		p.comments.MarkComments(ast.COLON)
	}
	p.expect(token.COLON)

	exp := ast.Property{
		Key:   value,
		Kind:  "value",
		Value: p.parseAssignmentExpression(),
	}

	if p.mode&StoreComments != 0 {
		p.comments.SetExpression(exp.Value)
	}
	return exp
}

func (p *parser) parseObjectLiteral() ast.Expression {
	var value []ast.Property
	idx0 := p.expect(token.LEFT_BRACE)
	for p.token != token.RIGHT_BRACE && p.token != token.EOF {
		value = append(value, p.parseObjectProperty())
		if p.token == token.COMMA {
			if p.mode&StoreComments != 0 {
				p.comments.Unset()
			}
			p.next()
			continue
		}
	}
	if p.mode&StoreComments != 0 {
		p.comments.MarkComments(ast.FINAL)
	}
	idx1 := p.expect(token.RIGHT_BRACE)

	return &ast.ObjectLiteral{
		LeftBrace:  idx0,
		RightBrace: idx1,
		Value:      value,
	}
}

func (p *parser) parseArrayLiteral() ast.Expression {
	idx0 := p.expect(token.LEFT_BRACKET)
	var value []ast.Expression
	for p.token != token.RIGHT_BRACKET && p.token != token.EOF {
		if p.token == token.COMMA {
			// This kind of comment requires a special empty expression node.
			empty := &ast.EmptyExpression{Begin: p.idx, End: p.idx}

			if p.mode&StoreComments != 0 {
				p.comments.SetExpression(empty)
				p.comments.Unset()
			}
			value = append(value, empty)
			p.next()
			continue
		}

		exp := p.parseAssignmentExpression()

		value = append(value, exp)
		if p.token != token.RIGHT_BRACKET {
			if p.mode&StoreComments != 0 {
				p.comments.Unset()
			}
			p.expect(token.COMMA)
		}
	}
	if p.mode&StoreComments != 0 {
		p.comments.MarkComments(ast.FINAL)
	}
	idx1 := p.expect(token.RIGHT_BRACKET)

	return &ast.ArrayLiteral{
		LeftBracket:  idx0,
		RightBracket: idx1,
		Value:        value,
	}
}

func (p *parser) parseArgumentList() (argumentList []ast.Expression, idx0, idx1 file.Idx) { //nolint:nonamedreturns
	if p.mode&StoreComments != 0 {
		p.comments.Unset()
	}
	idx0 = p.expect(token.LEFT_PARENTHESIS)
	for p.token != token.RIGHT_PARENTHESIS {
		exp := p.parseAssignmentExpression()
		if p.mode&StoreComments != 0 {
			p.comments.SetExpression(exp)
		}
		argumentList = append(argumentList, exp)
		if p.token != token.COMMA {
			break
		}
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()
	}
	if p.mode&StoreComments != 0 {
		p.comments.Unset()
	}
	idx1 = p.expect(token.RIGHT_PARENTHESIS)
	return
}

func (p *parser) parseCallExpression(left ast.Expression) ast.Expression {
	argumentList, idx0, idx1 := p.parseArgumentList()
	exp := &ast.CallExpression{
		Callee:           left,
		LeftParenthesis:  idx0,
		ArgumentList:     argumentList,
		RightParenthesis: idx1,
	}

	if p.mode&StoreComments != 0 {
		p.comments.SetExpression(exp)
	}
	return exp
}

func (p *parser) parseDotMember(left ast.Expression) ast.Expression {
	period := p.expect(token.PERIOD)

	literal := p.literal
	idx := p.idx

	if !matchIdentifier.MatchString(literal) {
		p.expect(token.IDENTIFIER)
		p.nextStatement()
		return &ast.BadExpression{From: period, To: p.idx}
	}

	p.next()

	return &ast.DotExpression{
		Left: left,
		Identifier: &ast.Identifier{
			Idx:  idx,
			Name: literal,
		},
	}
}

func (p *parser) parseBracketMember(left ast.Expression) ast.Expression {
	idx0 := p.expect(token.LEFT_BRACKET)
	member := p.parseExpression()
	idx1 := p.expect(token.RIGHT_BRACKET)
	return &ast.BracketExpression{
		LeftBracket:  idx0,
		Left:         left,
		Member:       member,
		RightBracket: idx1,
	}
}

func (p *parser) parseNewExpression() ast.Expression {
	idx := p.expect(token.NEW)
	callee := p.parseLeftHandSideExpression()
	node := &ast.NewExpression{
		New:    idx,
		Callee: callee,
	}
	if p.token == token.LEFT_PARENTHESIS {
		argumentList, idx0, idx1 := p.parseArgumentList()
		node.ArgumentList = argumentList
		node.LeftParenthesis = idx0
		node.RightParenthesis = idx1
	}

	if p.mode&StoreComments != 0 {
		p.comments.SetExpression(node)
	}

	return node
}

func (p *parser) parseLeftHandSideExpression() ast.Expression {
	var left ast.Expression
	if p.token == token.NEW {
		left = p.parseNewExpression()
	} else {
		if p.mode&StoreComments != 0 {
			p.comments.MarkComments(ast.LEADING)
			p.comments.MarkPrimary()
		}
		left = p.parsePrimaryExpression()
	}

	if p.mode&StoreComments != 0 {
		p.comments.SetExpression(left)
	}

	for {
		switch p.token {
		case token.PERIOD:
			left = p.parseDotMember(left)
		case token.LEFT_BRACKET:
			left = p.parseBracketMember(left)
		default:
			return left
		}
	}
}

func (p *parser) parseLeftHandSideExpressionAllowCall() ast.Expression {
	allowIn := p.scope.allowIn
	p.scope.allowIn = true
	defer func() {
		p.scope.allowIn = allowIn
	}()

	var left ast.Expression
	if p.token == token.NEW {
		var newComments []*ast.Comment
		if p.mode&StoreComments != 0 {
			newComments = p.comments.FetchAll()
			p.comments.MarkComments(ast.LEADING)
			p.comments.MarkPrimary()
		}
		left = p.parseNewExpression()
		if p.mode&StoreComments != 0 {
			p.comments.CommentMap.AddComments(left, newComments, ast.LEADING)
		}
	} else {
		if p.mode&StoreComments != 0 {
			p.comments.MarkComments(ast.LEADING)
			p.comments.MarkPrimary()
		}
		left = p.parsePrimaryExpression()
	}

	if p.mode&StoreComments != 0 {
		p.comments.SetExpression(left)
	}

	for {
		switch p.token {
		case token.PERIOD:
			left = p.parseDotMember(left)
		case token.LEFT_BRACKET:
			left = p.parseBracketMember(left)
		case token.LEFT_PARENTHESIS:
			left = p.parseCallExpression(left)
		default:
			return left
		}
	}
}

func (p *parser) parsePostfixExpression() ast.Expression {
	operand := p.parseLeftHandSideExpressionAllowCall()

	switch p.token {
	case token.INCREMENT, token.DECREMENT:
		// Make sure there is no line terminator here
		if p.implicitSemicolon {
			break
		}
		tkn := p.token
		idx := p.idx
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()
		switch operand.(type) {
		case *ast.Identifier, *ast.DotExpression, *ast.BracketExpression:
		default:
			p.error(idx, "invalid left-hand side in assignment")
			p.nextStatement()
			return &ast.BadExpression{From: idx, To: p.idx}
		}
		exp := &ast.UnaryExpression{
			Operator: tkn,
			Idx:      idx,
			Operand:  operand,
			Postfix:  true,
		}

		if p.mode&StoreComments != 0 {
			p.comments.SetExpression(exp)
		}

		return exp
	}

	return operand
}

func (p *parser) parseUnaryExpression() ast.Expression {
	switch p.token {
	case token.PLUS, token.MINUS, token.NOT, token.BITWISE_NOT:
		fallthrough
	case token.DELETE, token.VOID, token.TYPEOF:
		tkn := p.token
		idx := p.idx
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()

		return &ast.UnaryExpression{
			Operator: tkn,
			Idx:      idx,
			Operand:  p.parseUnaryExpression(),
		}
	case token.INCREMENT, token.DECREMENT:
		tkn := p.token
		idx := p.idx
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()
		operand := p.parseUnaryExpression()
		switch operand.(type) {
		case *ast.Identifier, *ast.DotExpression, *ast.BracketExpression:
		default:
			p.error(idx, "invalid left-hand side in assignment")
			p.nextStatement()
			return &ast.BadExpression{From: idx, To: p.idx}
		}
		return &ast.UnaryExpression{
			Operator: tkn,
			Idx:      idx,
			Operand:  operand,
		}
	}

	return p.parsePostfixExpression()
}

func (p *parser) parseMultiplicativeExpression() ast.Expression {
	next := p.parseUnaryExpression
	left := next()

	for p.token == token.MULTIPLY || p.token == token.SLASH ||
		p.token == token.REMAINDER {
		tkn := p.token
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()

		left = &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    next(),
		}
	}

	return left
}

func (p *parser) parseAdditiveExpression() ast.Expression {
	next := p.parseMultiplicativeExpression
	left := next()

	for p.token == token.PLUS || p.token == token.MINUS {
		tkn := p.token
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()

		left = &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    next(),
		}
	}

	return left
}

func (p *parser) parseShiftExpression() ast.Expression {
	next := p.parseAdditiveExpression
	left := next()

	for p.token == token.SHIFT_LEFT || p.token == token.SHIFT_RIGHT ||
		p.token == token.UNSIGNED_SHIFT_RIGHT {
		tkn := p.token
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()

		left = &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    next(),
		}
	}

	return left
}

func (p *parser) parseRelationalExpression() ast.Expression {
	next := p.parseShiftExpression
	left := next()

	allowIn := p.scope.allowIn
	p.scope.allowIn = true
	defer func() {
		p.scope.allowIn = allowIn
	}()

	switch p.token {
	case token.LESS, token.LESS_OR_EQUAL, token.GREATER, token.GREATER_OR_EQUAL:
		tkn := p.token
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()

		exp := &ast.BinaryExpression{
			Operator:   tkn,
			Left:       left,
			Right:      p.parseRelationalExpression(),
			Comparison: true,
		}
		return exp
	case token.INSTANCEOF:
		tkn := p.token
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()

		exp := &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    p.parseRelationalExpression(),
		}
		return exp
	case token.IN:
		if !allowIn {
			return left
		}
		tkn := p.token
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()

		exp := &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    p.parseRelationalExpression(),
		}
		return exp
	}

	return left
}

func (p *parser) parseEqualityExpression() ast.Expression {
	next := p.parseRelationalExpression
	left := next()

	for p.token == token.EQUAL || p.token == token.NOT_EQUAL ||
		p.token == token.STRICT_EQUAL || p.token == token.STRICT_NOT_EQUAL {
		tkn := p.token
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()

		left = &ast.BinaryExpression{
			Operator:   tkn,
			Left:       left,
			Right:      next(),
			Comparison: true,
		}
	}

	return left
}

func (p *parser) parseBitwiseAndExpression() ast.Expression {
	next := p.parseEqualityExpression
	left := next()

	for p.token == token.AND {
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		tkn := p.token
		p.next()

		left = &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    next(),
		}
	}

	return left
}

func (p *parser) parseBitwiseExclusiveOrExpression() ast.Expression {
	next := p.parseBitwiseAndExpression
	left := next()

	for p.token == token.EXCLUSIVE_OR {
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		tkn := p.token
		p.next()

		left = &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    next(),
		}
	}

	return left
}

func (p *parser) parseBitwiseOrExpression() ast.Expression {
	next := p.parseBitwiseExclusiveOrExpression
	left := next()

	for p.token == token.OR {
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		tkn := p.token
		p.next()

		left = &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    next(),
		}
	}

	return left
}

func (p *parser) parseLogicalAndExpression() ast.Expression {
	next := p.parseBitwiseOrExpression
	left := next()

	for p.token == token.LOGICAL_AND {
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		tkn := p.token
		p.next()

		left = &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    next(),
		}
	}

	return left
}

func (p *parser) parseLogicalOrExpression() ast.Expression {
	next := p.parseLogicalAndExpression
	left := next()

	for p.token == token.LOGICAL_OR {
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		tkn := p.token
		p.next()

		left = &ast.BinaryExpression{
			Operator: tkn,
			Left:     left,
			Right:    next(),
		}
	}

	return left
}

func (p *parser) parseConditionalExpression() ast.Expression {
	left := p.parseLogicalOrExpression()

	if p.token == token.QUESTION_MARK {
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()

		consequent := p.parseAssignmentExpression()
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.expect(token.COLON)
		exp := &ast.ConditionalExpression{
			Test:       left,
			Consequent: consequent,
			Alternate:  p.parseAssignmentExpression(),
		}

		return exp
	}

	return left
}

func (p *parser) parseAssignmentExpression() ast.Expression {
	left := p.parseConditionalExpression()
	var operator token.Token
	switch p.token {
	case token.ASSIGN:
		operator = p.token
	case token.ADD_ASSIGN:
		operator = token.PLUS
	case token.SUBTRACT_ASSIGN:
		operator = token.MINUS
	case token.MULTIPLY_ASSIGN:
		operator = token.MULTIPLY
	case token.QUOTIENT_ASSIGN:
		operator = token.SLASH
	case token.REMAINDER_ASSIGN:
		operator = token.REMAINDER
	case token.AND_ASSIGN:
		operator = token.AND
	case token.AND_NOT_ASSIGN:
		operator = token.AND_NOT
	case token.OR_ASSIGN:
		operator = token.OR
	case token.EXCLUSIVE_OR_ASSIGN:
		operator = token.EXCLUSIVE_OR
	case token.SHIFT_LEFT_ASSIGN:
		operator = token.SHIFT_LEFT
	case token.SHIFT_RIGHT_ASSIGN:
		operator = token.SHIFT_RIGHT
	case token.UNSIGNED_SHIFT_RIGHT_ASSIGN:
		operator = token.UNSIGNED_SHIFT_RIGHT
	}

	if operator != 0 {
		idx := p.idx
		if p.mode&StoreComments != 0 {
			p.comments.Unset()
		}
		p.next()
		switch left.(type) {
		case *ast.Identifier, *ast.DotExpression, *ast.BracketExpression:
		default:
			p.error(left.Idx0(), "invalid left-hand side in assignment")
			p.nextStatement()
			return &ast.BadExpression{From: idx, To: p.idx}
		}

		exp := &ast.AssignExpression{
			Left:     left,
			Operator: operator,
			Right:    p.parseAssignmentExpression(),
		}

		if p.mode&StoreComments != 0 {
			p.comments.SetExpression(exp)
		}

		return exp
	}

	return left
}

func (p *parser) parseExpression() ast.Expression {
	next := p.parseAssignmentExpression
	left := next()

	if p.token == token.COMMA {
		sequence := []ast.Expression{left}
		for {
			if p.token != token.COMMA {
				break
			}
			p.next()
			sequence = append(sequence, next())
		}
		return &ast.SequenceExpression{
			Sequence: sequence,
		}
	}

	return left
}
