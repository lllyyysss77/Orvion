```markdown
# Orvion Development Patterns

> Auto-generated skill from repository analysis

## Overview
This skill teaches the core development patterns and conventions used in the Orvion Go codebase. It covers file and code organization, commit message style, import/export practices, and testing patterns. Use this as a reference for contributing code that aligns with the project's standards.

## Coding Conventions

### File Naming
- Use **snake_case** for all file names.
  - Example: `user_service.go`, `data_parser.go`

### Import Style
- Use **alias imports** for external packages.
  - Example:
    ```go
    import (
        db "github.com/example/database"
        util "github.com/example/utils"
    )
    ```

### Export Style
- Use **named exports** for functions, types, and variables that should be accessible outside the package.
  - Example:
    ```go
    // Exported function
    func ProcessData(input string) error {
        // ...
    }

    // Exported type
    type User struct {
        ID   int
        Name string
    }
    ```

### Commit Messages
- Follow **conventional commit** patterns.
- Use prefixes like `refactor` and `feat`.
- Keep commit messages concise (average 30 characters).
  - Example:
    ```
    feat: add user authentication
    refactor: optimize data parsing
    ```

## Workflows

### Refactoring Code
**Trigger:** When improving code structure or performance without changing external behavior  
**Command:** `/refactor`

1. Identify code that can be improved (readability, performance, etc.)
2. Make changes while ensuring no breaking changes to the API
3. Use the `refactor:` prefix in your commit message
4. Run all tests to verify correctness
5. Submit a pull request for review

### Adding a New Feature
**Trigger:** When implementing new functionality  
**Command:** `/feat`

1. Define the feature requirements
2. Create new files using snake_case if needed
3. Use alias imports for dependencies
4. Export new functions/types as needed
5. Write or update tests in `*.test.*` files
6. Use the `feat:` prefix in your commit message
7. Run all tests to ensure correctness
8. Submit a pull request

## Testing Patterns

- Test files follow the pattern: `*.test.*`
  - Example: `user_service.test.go`
- Testing framework is **unknown**; follow existing test file structure.
- Place tests alongside the code they test, using the same naming conventions.
- Example test file structure:
    ```go
    package user

    import (
        "testing"
    )

    func TestProcessData(t *testing.T) {
        // test logic here
    }
    ```

## Commands
| Command   | Purpose                                 |
|-----------|-----------------------------------------|
| /refactor | Start a refactoring workflow            |
| /feat     | Start a new feature development workflow|
```
