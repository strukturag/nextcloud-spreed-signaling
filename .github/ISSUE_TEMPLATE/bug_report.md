---

name: Bug report
about: Create a report to help us improve
labels: bug

---

<!--- Please keep this note for other contributors -->

### How to use GitHub

* Please use the 👍 [reaction](https://blog.github.com/2016-03-10-add-reactions-to-pull-requests-issues-and-comments/) to show that you are interested into the same feature.
* Please don't comment if you have no relevant information to add. It's just extra noise for everyone subscribed to this issue.
* Subscribe to receive notifications on status change and new comments.

### Describe the bug
A clear and concise description of what the bug is.

#### Screenshots
If applicable, add screenshots to help explain your problem.

### Steps to reproduce
Steps to reproduce the behavior:
1. Go to '...'
2. Click on '....'
3. Scroll down to '....'
4. See error

### Expected behaviour
A clear and concise description of what you expected to happen.

### Server configuration

**Operating system:**

**Web server:**

**Database:**

**PHP version:**

**Nextcloud version:** (see Nextcloud admin page)

**Updated from an older Nextcloud/ownCloud or fresh install:**

**Where did you install Nextcloud from:**

**Signing status:**
<details>
<summary>Signing status</summary>

```
Login as admin user into your Nextcloud and access 
http://example.com/index.php/settings/integrity/failed 
paste the results here.
```
</details>

**List of activated apps:**
<details>
<summary>App list</summary>

```
If you have access to your command line run e.g.:
sudo -u www-data php occ app:list
from within your Nextcloud installation folder
```
</details>

**Nextcloud configuration:**
<details>
<summary>Config report</summary>

```
If you have access to your command line run e.g.:
sudo -u www-data php occ config:list system
from within your Nextcloud installation folder

or 

Insert your config.php content here. 
Make sure to remove all sensitive content such as passwords. (e.g. database password, passwordsalt, secret, smtp password, …)
```
</details>

**Are you using external storage, if yes which one:** local/smb/sftp/...

**Are you using encryption:** yes/no

**Are you using an external user-backend, if yes which one:** LDAP/ActiveDirectory/Webdav/...

#### LDAP configuration (delete this part if not used)
<details>
<summary>LDAP config</summary>

```
With access to your command line run e.g.:
sudo -u www-data php occ ldap:show-config
from within your Nextcloud installation folder

Without access to your command line download the data/owncloud.db to your local
computer or access your SQL server remotely and run the select query:
SELECT * FROM `oc_appconfig` WHERE `appid` = 'user_ldap';


Eventually replace sensitive data as the name/IP-address of your LDAP server or groups.
```
</details>

### Client configuration
**Browser:**
-   Browser (e.g. chrome, safari)
-   Version (e.g. 22)

**Operating system:**
-   Device: (e.g. iPhone6)
-   OS: (e.g. iOS)

### Logs

<!--- Reports without logs might be closed as unqualified reports! -->

#### Web server error log
<details>
<summary>Web server error log</summary>

```
Insert your webserver log here
```
</details>

#### Nextcloud log (data/nextcloud.log)
<details>
<summary>Nextcloud log</summary>

```
Insert your log here
```
</details>

#### Browser log
<details>
<summary>Browser log</summary>

```
Insert your browser log here, this could for example include:

a) The javascript console log
b) The network log
c) ...
```
</details>

### Additional context
Add any other context about the problem here.
