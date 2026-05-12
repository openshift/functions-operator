# Private Git Repository Authentication E2E Test Design

## Overview

Add end-to-end tests to verify that the func-operator correctly handles private Git repositories using authentication credentials specified via `.spec.repository.authSecretRef`.

## Background

The func-operator supports private Git repositories by allowing users to specify a reference to an authentication secret in the Function CR. The controller reads this secret and uses the credentials to clone the repository. The implementation in `internal/git/manager.go` supports two authentication methods:

1. **Token-based**: Secret contains `token` key
2. **Username/Password**: Secret contains `username` and `password` keys

Currently, there are no e2e tests verifying this functionality works end-to-end.

## Test Structure

All tests will be added to `test/e2e/func_deploy_test.go` to keep deployment-related tests consolidated.

### Test Contexts

Four new test contexts will be added:

1. **"with a private repository using token authentication - success"**
   - Verifies Function becomes Ready when authSecretRef is provided
   
2. **"with a private repository using token authentication - failure"**
   - Verifies Function fails with authentication error when authSecretRef is missing

3. **"with a private repository using username/password authentication - success"**
   - Verifies Function becomes Ready when authSecretRef is provided

4. **"with a private repository using username/password authentication - failure"**
   - Verifies Function fails with authentication error when authSecretRef is missing

## Implementation Details

### Test Flow (Token Authentication - Success)

**BeforeEach:**
1. Create random Gitea user using `repoProvider.CreateRandomUser()`
2. Create private repository using `repoProvider.CreateRandomRepo(username, true)`
3. Create access token using `repoProvider.CreateAccessToken(username, password, "e2e-token")`
4. Initialize repository with function code using `utils.InitializeRepoWithFunction()`
5. Deploy function using func CLI (authenticates with username/password for git operations)
6. Commit func.yaml changes
7. Create test namespace

**Test Body:**
1. Create Kubernetes Secret with token data using k8sClient:
   ```go
   secret := &v1.Secret{
       ObjectMeta: metav1.ObjectMeta{
           GenerateName: "git-auth-",
           Namespace:    functionNamespace,
       },
       Data: map[string][]byte{
           "token": []byte(token),
       },
   }
   ```
2. Create Function CR with `spec.repository.authSecretRef.name` pointing to the secret
3. Verify Function becomes Ready:
   ```go
   funcBecomeReady := func(g Gomega) {
       fn := &functionsdevv1alpha1.Function{}
       err := k8sClient.Get(ctx, types.NamespacedName{Name: functionName, Namespace: functionNamespace}, fn)
       g.Expect(err).NotTo(HaveOccurred())
       
       for _, cond := range fn.Status.Conditions {
           if cond.Type == functionsdevv1alpha1.TypeReady {
               g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
               return
           }
       }
       g.Expect(false).To(BeTrue(), "Ready condition not found")
   }
   Eventually(funcBecomeReady, 6*time.Minute).Should(Succeed())
   ```

**Cleanup:**
- Delete Function CR
- Delete Secret
- Cleanup namespace, repo, user (via DeferCleanup)

### Test Flow (Token Authentication - Failure)

**BeforeEach:** Same as success case

**Test Body:**
1. Do NOT create authentication secret
2. Create Function CR WITHOUT `spec.repository.authSecretRef`
3. Verify Function does NOT become Ready and has authentication error:
   ```go
   funcFailsWithAuthError := func(g Gomega) {
       fn := &functionsdevv1alpha1.Function{}
       err := k8sClient.Get(ctx, types.NamespacedName{Name: functionName, Namespace: functionNamespace}, fn)
       g.Expect(err).NotTo(HaveOccurred())
       
       // Check it's NOT Ready
       for _, cond := range fn.Status.Conditions {
           if cond.Type == functionsdevv1alpha1.TypeReady {
               g.Expect(cond.Status).NotTo(Equal(metav1.ConditionTrue))
           }
           // Check for SourceReady condition with auth error
           if cond.Type == functionsdevv1alpha1.TypeSourceReady {
               g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
               g.Expect(cond.Message).To(Or(
                   ContainSubstring("authentication"),
                   ContainSubstring("Authentication"),
                   ContainSubstring("401"),
                   ContainSubstring("Unauthorized"),
               ))
               return
           }
       }
       g.Expect(false).To(BeTrue(), "SourceReady condition not found")
   }
   Eventually(funcFailsWithAuthError, 2*time.Minute).Should(Succeed())
   ```

**Cleanup:** Same as success case

### Test Flow (Username/Password Authentication)

Same as token authentication, but:
- No token creation
- Secret contains username and password:
  ```go
  secret := &v1.Secret{
      ObjectMeta: metav1.ObjectMeta{
          GenerateName: "git-auth-",
          Namespace:    functionNamespace,
      },
      Data: map[string][]byte{
          "username": []byte(username),
          "password": []byte(password),
      },
  }
  ```

## Verification Criteria

### Positive Tests
- Function CR is created successfully
- Function status reaches Ready condition with status=True
- No error conditions present
- Timeout: 6 minutes (matching existing deployed function tests)

### Negative Tests
- Function CR is created successfully
- Function status does NOT reach Ready condition
- SourceReady condition exists with status=False
- SourceReady condition message contains authentication-related error keywords
- Timeout: 2 minutes (should fail faster)

## Testing Infrastructure Requirements

All required infrastructure already exists:
- Gitea client with user/repo/token management in `test/utils/gitea.go`
- Repository initialization helpers in `test/utils/git.go`
- k8sClient available in test suite
- Test namespace management

## Future Enhancements

Potential follow-up work (not in scope for this implementation):
- Refactor duplicated `funcBecomeReady` closures into reusable test helpers
- Test SSH-based authentication (if/when implemented)
- Test secret updates/rotation
- Test invalid credentials handling